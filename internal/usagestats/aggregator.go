// Package usagestats aggregates per-request usage records (tokens, latency)
// into rolling time buckets and exposes them via the management API. It is a
// read-only sidecar registered alongside the redisqueue usage plugin; it never
// mutates request/routing behaviour.
package usagestats

import (
	"context"
	"sync"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const (
	bucketSeconds         = 60
	defaultRetentionHours = 48
	latencyBinCount       = 9
)

// fixed upper bounds (ms) for the latency histogram; the last bin is +inf.
var latencyBinsMs = [latencyBinCount - 1]int64{50, 100, 200, 400, 800, 1600, 3200, 6400}

type tokenSums struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cached    int64 `json:"cached"`
	Total     int64 `json:"total"`
}

type bucketAgg struct {
	Requests     int64                  `json:"requests"`
	Success      int64                  `json:"success"`
	Failed       int64                  `json:"failed"`
	Tokens       tokenSums              `json:"tokens"`
	LatencySumMs int64                  `json:"latency_sum_ms"`
	LatencyCount int64                  `json:"latency_count"`
	TTFTSumMs    int64                  `json:"ttft_sum_ms"`
	TTFTCount    int64                  `json:"ttft_count"`
	LatencyHist  [latencyBinCount]int64 `json:"latency_hist"`
}

type minuteBucket struct {
	Overall      bucketAgg             `json:"overall"`
	ByProvider   map[string]*bucketAgg `json:"by_provider"`
	ByModel      map[string]*bucketAgg `json:"by_model"`
	ByCredential map[string]*bucketAgg `json:"by_credential"`
	ByAPIKey     map[string]*bucketAgg `json:"by_apikey"`
}

func newMinuteBucket() *minuteBucket {
	return &minuteBucket{
		ByProvider:   map[string]*bucketAgg{},
		ByModel:      map[string]*bucketAgg{},
		ByCredential: map[string]*bucketAgg{},
		ByAPIKey:     map[string]*bucketAgg{},
	}
}

// Aggregator is the package-level singleton usage sink.
type Aggregator struct {
	mu               sync.Mutex
	enabled          bool
	retentionHours   int
	buckets          map[int64]*minuteBucket // key = unix minute (unix/60)
	snapshotPath     string
	snapshotInterval time.Duration
}

var global = newAggregator()

func newAggregator() *Aggregator {
	return &Aggregator{retentionHours: defaultRetentionHours, buckets: map[int64]*minuteBucket{}}
}

// GetAggregator returns the singleton (used by the management handler).
func GetAggregator() *Aggregator { return global }

func init() { coreusage.RegisterPlugin(global) }

func addInto(b *bucketAgg, r coreusage.Record) {
	b.Requests++
	if r.Failed {
		b.Failed++
	} else {
		b.Success++
	}
	b.Tokens.Input += r.Detail.InputTokens
	b.Tokens.Output += r.Detail.OutputTokens
	b.Tokens.Reasoning += r.Detail.ReasoningTokens
	b.Tokens.Cached += r.Detail.CachedTokens
	b.Tokens.Total += r.Detail.TotalTokens
	if latMs := r.Latency.Milliseconds(); latMs > 0 {
		b.LatencySumMs += latMs
		b.LatencyCount++
		b.LatencyHist[latencyBin(latMs)]++
	}
	if t := r.TTFT.Milliseconds(); t > 0 {
		b.TTFTSumMs += t
		b.TTFTCount++
	}
}

func latencyBin(ms int64) int {
	for i, ub := range latencyBinsMs {
		if ms <= ub {
			return i
		}
	}
	return latencyBinCount - 1
}

func dimAdd(m map[string]*bucketAgg, key string, r coreusage.Record) {
	if key == "" {
		key = "(unknown)"
	}
	b := m[key]
	if b == nil {
		b = &bucketAgg{}
		m[key] = b
	}
	addInto(b, r)
}

// HandleUsage implements coreusage.Plugin. Cheap, non-blocking, panic-safe.
func (a *Aggregator) HandleUsage(_ context.Context, r coreusage.Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.enabled {
		return
	}
	ts := r.RequestedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	minute := ts.Unix() / bucketSeconds
	mb := a.buckets[minute]
	if mb == nil {
		mb = newMinuteBucket()
		a.buckets[minute] = mb
	}
	addInto(&mb.Overall, r)
	dimAdd(mb.ByProvider, r.Provider, r)
	dimAdd(mb.ByModel, modelKey(r), r)
	dimAdd(mb.ByCredential, r.AuthIndex, r)
	dimAdd(mb.ByAPIKey, r.APIKey, r)
}

func modelKey(r coreusage.Record) string {
	if r.Alias != "" {
		return r.Alias
	}
	return r.Model
}

// totalRequests is a test helper.
func (a *Aggregator) totalRequests() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var n int64
	for _, mb := range a.buckets {
		n += mb.Overall.Requests
	}
	return n
}
