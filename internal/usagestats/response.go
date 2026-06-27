package usagestats

import (
	"sort"
	"time"
)

// Latency holds avg/p50/p95 in milliseconds.
type Latency struct {
	Avg int64 `json:"avg"`
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
}

// Totals are the window-wide aggregates.
type Totals struct {
	Requests        int64   `json:"requests"`
	Success         int64   `json:"success"`
	Failed          int64   `json:"failed"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	LatencyMs       Latency `json:"latency_ms"`
	TTFTMs          Latency `json:"ttft_ms"`
}

// SeriesPoint is one downsampled time bucket of the overall throughput.
type SeriesPoint struct {
	T            int64 `json:"t"`
	Requests     int64 `json:"requests"`
	Success      int64 `json:"success"`
	Failed       int64 `json:"failed"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	LatencyAvgMs int64 `json:"latency_avg_ms"`
}

// GroupStat is a per-dimension breakdown row.
type GroupStat struct {
	Key          string `json:"key"`
	Requests     int64  `json:"requests"`
	Success      int64  `json:"success"`
	Failed       int64  `json:"failed"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
	LatencyAvgMs int64  `json:"latency_avg_ms"`
	LatencyP95Ms int64  `json:"latency_p95_ms"`
	TTFTAvgMs    int64  `json:"ttft_avg_ms"`
}

// StatsResponse is the /usage-stats payload.
type StatsResponse struct {
	Available     bool          `json:"available"`
	Window        string        `json:"window"`
	BucketSeconds int           `json:"bucket_seconds"`
	GeneratedAt   int64         `json:"generated_at"`
	Totals        Totals        `json:"totals"`
	Series        []SeriesPoint `json:"series"`
	Groups        []GroupStat   `json:"groups"`
}

func avg(sum, n int64) int64 {
	if n == 0 {
		return 0
	}
	return sum / n
}

func dimMap(mb *minuteBucket, group string) map[string]*bucketAgg {
	switch group {
	case "provider":
		return mb.ByProvider
	case "credential":
		return mb.ByCredential
	case "apikey":
		return mb.ByAPIKey
	default:
		return mb.ByModel
	}
}

// BuildResponse aggregates buckets within [now-window, now] into totals, a
// series downsampled to bucketSec, and a per-group breakdown.
func (a *Aggregator) BuildResponse(window time.Duration, bucketSec int, group string, now time.Time) StatsResponse {
	a.mu.Lock()
	defer a.mu.Unlock()
	if bucketSec < bucketSeconds {
		bucketSec = bucketSeconds
	}
	startMin := now.Add(-window).Unix() / bucketSeconds
	endMin := now.Unix() / bucketSeconds

	var tot bucketAgg
	groupAcc := map[string]*bucketAgg{}
	seriesMap := map[int64]*SeriesPoint{}

	for min := startMin; min <= endMin; min++ {
		mb := a.buckets[min]
		if mb == nil {
			continue
		}
		mergeInto(&tot, &mb.Overall)

		sk := (min * bucketSeconds) / int64(bucketSec) * int64(bucketSec)
		sp := seriesMap[sk]
		if sp == nil {
			sp = &SeriesPoint{T: sk}
			seriesMap[sk] = sp
		}
		sp.Requests += mb.Overall.Requests
		sp.Success += mb.Overall.Success
		sp.Failed += mb.Overall.Failed
		sp.InputTokens += mb.Overall.Tokens.Input
		sp.OutputTokens += mb.Overall.Tokens.Output
		sp.TotalTokens += mb.Overall.Tokens.Total
		if mb.Overall.LatencyCount > 0 {
			sp.LatencyAvgMs = avg(mb.Overall.LatencySumMs, mb.Overall.LatencyCount)
		}

		for k, b := range dimMap(mb, group) {
			g := groupAcc[k]
			if g == nil {
				g = &bucketAgg{}
				groupAcc[k] = g
			}
			mergeInto(g, b)
		}
	}

	resp := StatsResponse{
		Available:     true,
		Window:        window.String(),
		BucketSeconds: bucketSec,
		GeneratedAt:   now.Unix(),
		Series:        []SeriesPoint{},
		Groups:        []GroupStat{},
		Totals: Totals{
			Requests: tot.Requests, Success: tot.Success, Failed: tot.Failed,
			InputTokens: tot.Tokens.Input, OutputTokens: tot.Tokens.Output,
			ReasoningTokens: tot.Tokens.Reasoning, CachedTokens: tot.Tokens.Cached,
			TotalTokens: tot.Tokens.Total,
			LatencyMs: Latency{Avg: avg(tot.LatencySumMs, tot.LatencyCount),
				P50: percentileFromHist(tot.LatencyHist, 0.50), P95: percentileFromHist(tot.LatencyHist, 0.95)},
			TTFTMs: Latency{Avg: avg(tot.TTFTSumMs, tot.TTFTCount),
				P50: 0, P95: 0},
		},
	}

	for _, sp := range seriesMap {
		resp.Series = append(resp.Series, *sp)
	}
	sort.Slice(resp.Series, func(i, j int) bool { return resp.Series[i].T < resp.Series[j].T })

	for k, b := range groupAcc {
		resp.Groups = append(resp.Groups, GroupStat{
			Key: k, Requests: b.Requests, Success: b.Success, Failed: b.Failed,
			InputTokens: b.Tokens.Input, OutputTokens: b.Tokens.Output, TotalTokens: b.Tokens.Total,
			LatencyAvgMs: avg(b.LatencySumMs, b.LatencyCount),
			LatencyP95Ms: percentileFromHist(b.LatencyHist, 0.95),
			TTFTAvgMs:    avg(b.TTFTSumMs, b.TTFTCount),
		})
	}
	sort.Slice(resp.Groups, func(i, j int) bool { return resp.Groups[i].Requests > resp.Groups[j].Requests })
	return resp
}

func mergeInto(dst, src *bucketAgg) {
	dst.Requests += src.Requests
	dst.Success += src.Success
	dst.Failed += src.Failed
	dst.Tokens.Input += src.Tokens.Input
	dst.Tokens.Output += src.Tokens.Output
	dst.Tokens.Reasoning += src.Tokens.Reasoning
	dst.Tokens.Cached += src.Tokens.Cached
	dst.Tokens.Total += src.Tokens.Total
	dst.LatencySumMs += src.LatencySumMs
	dst.LatencyCount += src.LatencyCount
	dst.TTFTSumMs += src.TTFTSumMs
	dst.TTFTCount += src.TTFTCount
	for i := range dst.LatencyHist {
		dst.LatencyHist[i] += src.LatencyHist[i]
	}
}
