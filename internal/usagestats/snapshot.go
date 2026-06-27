package usagestats

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type snapshotFile struct {
	Version int                     `json:"version"`
	Buckets map[int64]*minuteBucket `json:"buckets"`
	SavedAt int64                   `json:"saved_at"`
}

// Configure applies runtime config; call once at server start.
func (a *Aggregator) Configure(enabled bool, retentionHours, snapshotIntervalSec int, path string) {
	a.mu.Lock()
	a.enabled = enabled
	if retentionHours > 0 {
		a.retentionHours = retentionHours
	}
	a.snapshotPath = path
	a.snapshotInterval = time.Duration(snapshotIntervalSec) * time.Second
	a.mu.Unlock()
	if enabled && path != "" {
		_ = a.loadSnapshot()
	}
}

func (a *Aggregator) pruneLocked(now time.Time) {
	cutoff := now.Add(-time.Duration(a.retentionHours)*time.Hour).Unix() / bucketSeconds
	for k := range a.buckets {
		if k < cutoff {
			delete(a.buckets, k)
		}
	}
}

func (a *Aggregator) saveSnapshot() error {
	a.mu.Lock()
	path := a.snapshotPath
	if path == "" {
		a.mu.Unlock()
		return nil
	}
	a.pruneLocked(time.Now())
	snap := snapshotFile{Version: 1, Buckets: a.buckets, SavedAt: time.Now().Unix()}
	data, err := json.Marshal(snap)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic replace
}

func (a *Aggregator) loadSnapshot() error {
	data, err := os.ReadFile(a.snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snap snapshotFile
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil // corrupt snapshot: start fresh rather than crash
	}
	a.mu.Lock()
	if snap.Buckets != nil {
		a.buckets = snap.Buckets
		for _, mb := range a.buckets {
			ensureMaps(mb)
		}
		a.pruneLocked(time.Now())
	}
	a.mu.Unlock()
	return nil
}

func ensureMaps(mb *minuteBucket) {
	if mb.ByProvider == nil {
		mb.ByProvider = map[string]*bucketAgg{}
	}
	if mb.ByModel == nil {
		mb.ByModel = map[string]*bucketAgg{}
	}
	if mb.ByCredential == nil {
		mb.ByCredential = map[string]*bucketAgg{}
	}
	if mb.ByAPIKey == nil {
		mb.ByAPIKey = map[string]*bucketAgg{}
	}
}

// StartSnapshotLoop periodically persists + prunes until ctx is cancelled.
func (a *Aggregator) StartSnapshotLoop(ctx context.Context) {
	a.mu.Lock()
	interval := a.snapshotInterval
	enabled := a.enabled && a.snapshotPath != ""
	a.mu.Unlock()
	if !enabled || interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = a.saveSnapshot()
				return
			case <-t.C:
				_ = a.saveSnapshot()
			}
		}
	}()
}
