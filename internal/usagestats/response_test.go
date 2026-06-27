package usagestats

import (
	"context"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestBuildResponseGroupsByModel(t *testing.T) {
	a := newAggregator()
	a.enabled = true
	base := time.Unix(1_700_000_400, 0)
	mk := func(model string, in, out int64) coreusage.Record {
		return coreusage.Record{Provider: "claude", Model: model, AuthIndex: "i", APIKey: "k",
			RequestedAt: base, Latency: 100 * time.Millisecond,
			Detail: coreusage.Detail{InputTokens: in, OutputTokens: out, TotalTokens: in + out}}
	}
	a.HandleUsage(context.Background(), mk("gpt-5.5", 10, 5))
	a.HandleUsage(context.Background(), mk("gpt-5.5", 20, 5))
	a.HandleUsage(context.Background(), mk("opus", 1, 1))

	resp := a.BuildResponse(time.Hour, 60, "model", base.Add(time.Minute))
	if resp.Totals.Requests != 3 {
		t.Fatalf("totals.requests = %d, want 3", resp.Totals.Requests)
	}
	if resp.Totals.TotalTokens != 42 {
		t.Fatalf("totals.total_tokens = %d, want 42", resp.Totals.TotalTokens)
	}
	var gpt *GroupStat
	for i := range resp.Groups {
		if resp.Groups[i].Key == "gpt-5.5" {
			gpt = &resp.Groups[i]
		}
	}
	if gpt == nil || gpt.Requests != 2 || gpt.TotalTokens != 40 {
		t.Fatalf("gpt-5.5 group wrong: %+v", gpt)
	}
	// groups sorted by requests desc -> gpt-5.5 first
	if len(resp.Groups) != 2 || resp.Groups[0].Key != "gpt-5.5" {
		t.Fatalf("group order wrong: %+v", resp.Groups)
	}
}

func TestBuildResponseWindowExcludesOld(t *testing.T) {
	a := newAggregator()
	a.enabled = true
	old := time.Unix(1_700_000_000, 0)
	recent := old.Add(3 * time.Hour)
	a.HandleUsage(context.Background(), coreusage.Record{Provider: "p", Model: "m", RequestedAt: old,
		Detail: coreusage.Detail{TotalTokens: 99}})
	a.HandleUsage(context.Background(), coreusage.Record{Provider: "p", Model: "m", RequestedAt: recent,
		Detail: coreusage.Detail{TotalTokens: 1}})
	resp := a.BuildResponse(time.Hour, 60, "model", recent.Add(time.Minute))
	if resp.Totals.Requests != 1 || resp.Totals.TotalTokens != 1 {
		t.Fatalf("window filter wrong: %+v", resp.Totals)
	}
}
