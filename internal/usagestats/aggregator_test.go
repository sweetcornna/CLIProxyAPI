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
