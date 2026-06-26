package handlers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/net/context"
)

// compatNudgeMarker is a stable substring of the default nudge, used to keep the
// injection idempotent (never double-inject across retries/replays).
const compatNudgeMarker = "Sub-agent dispatch:"

// claudeCompatSubagentNudge is the relay-injected sub-agent dispatch nudge. It is
// deliberately minimal and scoped to dispatch discipline so it complements — never
// restates or overrides — Claude Code's own system prompt (which owns plan mode,
// skills, hooks, background tasks, auto-compact, todos, and the depth-5 recursion
// cap). Worded as preference, not prohibition, so it never blocks legitimate
// parallel fan-out.
const claudeCompatSubagentNudge = `Sub-agent dispatch: dispatch a sub-agent (the Task/Agent tool) only for genuinely independent work — two or more subtasks that can run in parallel without shared state or sequential dependencies. Do single tasks, and steps that must run in order, yourself directly. If you are already running as a dispatched sub-agent, strongly prefer doing the work yourself over dispatching further sub-agents. This preserves useful parallel fan-out while avoiding unnecessary layers of delegation.`

// dispatchToolNames are the tool names Claude Code-style clients use to spawn
// sub-agents (matched case-insensitively). "Agent" is the current name; "Task" is
// the legacy alias; "dispatch_agent" covers older variants.
var dispatchToolNames = map[string]struct{}{
	"task":           {},
	"agent":          {},
	"dispatch_agent": {},
}

// requestHasDispatchTool reports whether the Anthropic-format request body exposes
// a sub-agent dispatch tool in its tools array.
func requestHasDispatchTool(payload []byte) bool {
	tools := gjson.GetBytes(payload, "tools")
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, t gjson.Result) bool {
		name := strings.ToLower(strings.TrimSpace(t.Get("name").String()))
		if _, ok := dispatchToolNames[name]; ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// applyClaudeCodeCompat injects the sub-agent dispatch nudge into an Anthropic-format
// request, but only when a Claude Code client (claude-* requested model) has been
// mapped to a non-Claude (GPT) upstream and the request exposes a dispatch tool. The
// nudge rides in the first user message as a <system-reminder> — the same channel
// Claude Code uses for ephemeral guidance — so the downstream model treats it
// natively. Real (unmapped) Claude requests are left untouched.
func (h *BaseAPIHandler) applyClaudeCodeCompat(ctx context.Context, entryProtocol, originalModel, mappedModel string, rawJSON []byte) []byte {
	_ = ctx
	if h == nil || h.Cfg == nil || len(rawJSON) == 0 {
		return rawJSON
	}
	if !strings.EqualFold(entryProtocol, "claude") {
		return rawJSON
	}
	// Only target Claude Code → GPT: requested a claude-* model, mapped to non-claude.
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(originalModel)), "claude") {
		return rawJSON
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mappedModel)), "claude") {
		return rawJSON
	}
	if !h.Cfg.ClaudeCompat.SubagentNudgeEnabled() {
		return rawJSON
	}
	if !requestHasDispatchTool(rawJSON) {
		return rawJSON
	}
	text := strings.TrimSpace(h.Cfg.ClaudeCompat.SubagentNudgeText)
	if text == "" {
		text = claudeCompatSubagentNudge
	}
	return prependSystemReminderToFirstUserMessage(rawJSON, text)
}

// prependSystemReminderToFirstUserMessage prepends a standalone <system-reminder>
// block to the first user message of an Anthropic-format request. It handles both
// string and array content shapes and is idempotent on the default nudge.
func prependSystemReminderToFirstUserMessage(payload []byte, reminder string) []byte {
	reminder = strings.TrimSpace(reminder)
	if reminder == "" {
		return payload
	}
	messages := gjson.GetBytes(payload, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return payload
	}
	firstUserIdx := -1
	messages.ForEach(func(idx, msg gjson.Result) bool {
		if msg.Get("role").String() == "user" {
			firstUserIdx = int(idx.Int())
			return false
		}
		return true
	})
	if firstUserIdx < 0 {
		return payload
	}
	block := fmt.Sprintf("<system-reminder>\n%s\n</system-reminder>", reminder)
	contentPath := fmt.Sprintf("messages.%d.content", firstUserIdx)
	content := gjson.GetBytes(payload, contentPath)

	// Idempotency: never double-inject.
	if strings.Contains(content.Raw, compatNudgeMarker) {
		return payload
	}

	switch {
	case content.IsArray():
		nb, err := json.Marshal(map[string]any{"type": "text", "text": block})
		if err != nil {
			return payload
		}
		raw := strings.TrimSpace(content.Raw)
		var newArray string
		if raw == "[]" || raw == "" {
			newArray = "[" + string(nb) + "]"
		} else {
			newArray = "[" + string(nb) + "," + raw[1:]
		}
		if updated, errSet := sjson.SetRawBytes(payload, contentPath, []byte(newArray)); errSet == nil {
			payload = updated
		}
	case content.Type == gjson.String:
		if updated, errSet := sjson.SetBytes(payload, contentPath, block+"\n\n"+content.String()); errSet == nil {
			payload = updated
		}
	}
	return payload
}
