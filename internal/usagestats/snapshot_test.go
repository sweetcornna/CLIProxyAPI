package usagestats

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage-stats.json")
	a := newAggregator()
	a.enabled = true
	a.snapshotPath = path
	a.HandleUsage(context.Background(), coreusage.Record{Provider: "p", Model: "m",
		RequestedAt: time.Now(), Detail: coreusage.Detail{TotalTokens: 7}})
	if err := a.saveSnapshot(); err != nil {
		t.Fatal(err)
	}
	b := newAggregator()
	b.snapshotPath = path
	if err := b.loadSnapshot(); err != nil {
		t.Fatal(err)
	}
	if b.totalRequests() != 1 {
		t.Fatalf("restored requests = %d, want 1", b.totalRequests())
	}
	// nested dim maps must be usable after restore
	resp := b.BuildResponse(time.Hour, 60, "model", time.Now())
	if resp.Totals.TotalTokens != 7 {
		t.Fatalf("restored total_tokens = %d, want 7", resp.Totals.TotalTokens)
	}
}

func TestPruneDropsOld(t *testing.T) {
	a := newAggregator()
	a.enabled = true
	a.retentionHours = 1
	old := time.Unix(1_700_000_000, 0)
	a.HandleUsage(context.Background(), coreusage.Record{Provider: "p", Model: "m", RequestedAt: old,
		Detail: coreusage.Detail{TotalTokens: 1}})
	a.mu.Lock()
	a.pruneLocked(old.Add(2 * time.Hour))
	a.mu.Unlock()
	if a.totalRequests() != 0 {
		t.Fatalf("expected pruned to 0, got %d", a.totalRequests())
	}
}
