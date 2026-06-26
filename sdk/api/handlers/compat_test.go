package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/tidwall/gjson"
)

func TestRequestHasDispatchTool(t *testing.T) {
	with := []byte(`{"tools":[{"name":"Read"},{"name":"Agent"}]}`)
	withTask := []byte(`{"tools":[{"name":"Task"}]}`)
	without := []byte(`{"tools":[{"name":"Read"},{"name":"Bash"}]}`)
	none := []byte(`{"model":"x"}`)
	if !requestHasDispatchTool(with) || !requestHasDispatchTool(withTask) {
		t.Errorf("should detect Agent/Task dispatch tool")
	}
	if requestHasDispatchTool(without) || requestHasDispatchTool(none) {
		t.Errorf("should not detect dispatch tool when absent")
	}
}

func TestPrependSystemReminder_ArrayContent(t *testing.T) {
	in := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	out := prependSystemReminderToFirstUserMessage(in, "NUDGE-TEXT")
	first := gjson.GetBytes(out, "messages.0.content.0.text").String()
	if !strings.Contains(first, "NUDGE-TEXT") || !strings.Contains(first, "<system-reminder>") {
		t.Fatalf("reminder not prepended as first block: %s", out)
	}
	if gjson.GetBytes(out, "messages.0.content.1.text").String() != "hello" {
		t.Fatalf("original content should be preserved after the reminder block")
	}
	// Idempotent for the default nudge (which carries the marker substring).
	dn := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	once := prependSystemReminderToFirstUserMessage(dn, claudeCompatSubagentNudge)
	twice := prependSystemReminderToFirstUserMessage(once, claudeCompatSubagentNudge)
	if strings.Count(string(twice), compatNudgeMarker) != 1 {
		t.Errorf("default nudge should be idempotent on array content, got %d markers", strings.Count(string(twice), compatNudgeMarker))
	}
}

func TestPrependSystemReminder_StringContent(t *testing.T) {
	in := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	out := prependSystemReminderToFirstUserMessage(in, claudeCompatSubagentNudge)
	got := gjson.GetBytes(out, "messages.0.content").String()
	if !strings.Contains(got, "<system-reminder>") || !strings.Contains(got, "hello") {
		t.Fatalf("string content reminder failed: %s", got)
	}
	// Idempotent: default marker present → no second injection.
	out2 := string(prependSystemReminderToFirstUserMessage(out, claudeCompatSubagentNudge))
	if strings.Count(out2, compatNudgeMarker) != 1 {
		t.Errorf("default nudge should be idempotent, got %d markers", strings.Count(out2, compatNudgeMarker))
	}
}

func TestApplyClaudeCodeCompat_Gating(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{}}
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Agent"}]}`)

	// Fires: claude requested → gpt mapped, dispatch tool present, default-enabled.
	out := h.applyClaudeCodeCompat(context.Background(), "claude", "claude-sonnet-4-5", "gpt-5.5", body)
	if !strings.Contains(string(out), compatNudgeMarker) {
		t.Errorf("nudge should fire for Claude Code → GPT with dispatch tool")
	}

	// No-op: not mapped (still claude upstream).
	out = h.applyClaudeCodeCompat(context.Background(), "claude", "claude-sonnet-4-5", "claude-sonnet-4-5", body)
	if strings.Contains(string(out), compatNudgeMarker) {
		t.Errorf("nudge must not fire for real (unmapped) claude requests")
	}

	// No-op: no dispatch tool.
	noTool := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Read"}]}`)
	out = h.applyClaudeCodeCompat(context.Background(), "claude", "claude-opus-4", "gpt-5.5", noTool)
	if strings.Contains(string(out), compatNudgeMarker) {
		t.Errorf("nudge must not fire without a dispatch tool")
	}

	// No-op: explicitly disabled.
	disabled := false
	h2 := &BaseAPIHandler{Cfg: &config.SDKConfig{ClaudeCompat: config.CompatConfig{Enabled: &disabled}}}
	out = h2.applyClaudeCodeCompat(context.Background(), "claude", "claude-opus-4", "gpt-5.5", body)
	if strings.Contains(string(out), compatNudgeMarker) {
		t.Errorf("nudge must not fire when disabled")
	}

	// No-op: non-claude entry protocol (e.g. native openai client).
	out = h.applyClaudeCodeCompat(context.Background(), "openai", "claude-opus-4", "gpt-5.5", body)
	if strings.Contains(string(out), compatNudgeMarker) {
		t.Errorf("nudge must not fire for non-claude entry protocol")
	}
}
