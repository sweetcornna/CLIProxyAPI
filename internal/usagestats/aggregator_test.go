package usagestats

import (
	"context"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func rec(model string, in, out int64, lat time.Duration, failed bool) coreusage.Record {
	return coreusage.Record{
		Provider: "claude", Model: model, AuthIndex: "idx1", APIKey: "sk-1",
		RequestedAt: time.Unix(1_700_000_000, 0), Latency: lat, TTFT: lat / 2,
		Failed: failed, Detail: coreusage.Detail{InputTokens: in, OutputTokens: out, TotalTokens: in + out},
	}
}

func TestHandleUsageCounts(t *testing.T) {
	a := newAggregator()
	a.enabled = true
	a.HandleUsage(context.Background(), rec("gpt-5.5", 10, 5, 100*time.Millisecond, false))
	a.HandleUsage(context.Background(), rec("gpt-5.5", 20, 7, 200*time.Millisecond, true))
	if got := a.totalRequests(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestPercentileFromHist(t *testing.T) {
	var h [latencyBinCount]int64
	for i := 0; i < 9; i++ {
		h[0]++ // 9 samples in bin 0 (<=50ms)
	}
	h[4]++ // 1 sample in bin 4 (<=800ms)
	if p := percentileFromHist(h, 0.95); p != 800 {
		t.Fatalf("p95 = %d, want 800 (upper bound of bin 4)", p)
	}
	if p := percentileFromHist(h, 0.50); p != 50 {
		t.Fatalf("p50 = %d, want 50", p)
	}
	var empty [latencyBinCount]int64
	if p := percentileFromHist(empty, 0.95); p != 0 {
		t.Fatalf("empty p95 = %d, want 0", p)
	}
}
