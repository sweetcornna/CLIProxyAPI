package helps

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const ClaudeCodeSessionHeader = "X-Claude-Code-Session-Id"

var claudeCodeSessionSuffixPattern = regexp.MustCompile(`(?i)_session_([a-f0-9-]+)$`)

// ExtractClaudeCodeSessionID resolves a Claude Code session ID, preferring X-Claude-Code-Session-Id over payload metadata.
func ExtractClaudeCodeSessionID(ctx context.Context, payload []byte, headers http.Header) string {
	if headers != nil {
		if sessionID := strings.TrimSpace(headers.Get(ClaudeCodeSessionHeader)); sessionID != "" {
			return sessionID
		}
	}
	if ctx != nil {
		if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
			if sessionID := strings.TrimSpace(ginCtx.Request.Header.Get(ClaudeCodeSessionHeader)); sessionID != "" {
				return sessionID
			}
		}
	}
	return extractClaudeCodeSessionIDFromPayload(payload)
}

func extractClaudeCodeSessionIDFromPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	userID := gjson.GetBytes(payload, "metadata.user_id").String()
	if userID == "" {
		return ""
	}
	if matches := claudeCodeSessionSuffixPattern.FindStringSubmatch(userID); len(matches) >= 2 {
		return matches[1]
	}
	if len(userID) > 0 && userID[0] == '{' {
		return strings.TrimSpace(gjson.Get(userID, "session_id").String())
	}
	return ""
}

// ClaudeCodePromptCache maps a Claude Code session to a stable upstream prompt_cache_key.
func ClaudeCodePromptCache(ctx context.Context, _ string, payload []byte, headers http.Header) (CodexCache, bool, error) {
	sessionID := ExtractClaudeCodeSessionID(ctx, payload, headers)
	if sessionID == "" {
		if cacheID := ClaudeCodeCacheControlPromptCacheKey(payload); cacheID != "" {
			return CodexCache{
				ID:     cacheID,
				Expire: time.Now().Add(1 * time.Hour),
			}, true, nil
		}
		return ClaudeCodeDigestPromptCache(ctx, payload)
	}
	key := ClaudeCodePromptCacheKey(sessionID)
	if cache, ok, errCache := GetCodexCacheRequired(ctx, key); errCache != nil || ok {
		return cache, ok, errCache
	}
	cache := CodexCache{
		ID:     uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:claude-code-prompt-cache:"+sessionID)).String(),
		Expire: time.Now().Add(1 * time.Hour),
	}
	if errSet := SetCodexCacheRequired(ctx, key, cache); errSet != nil {
		return CodexCache{}, false, errSet
	}
	return cache, true, nil
}

// ClaudeCodeDigestPromptCache preserves prompt cache affinity when Claude Code
// session metadata and cache_control anchors are unavailable. It mirrors the
// sub2api digest-chain fallback: a later request whose message digest extends a
// prior chain reuses the prior prompt_cache_key.
func ClaudeCodeDigestPromptCache(ctx context.Context, payload []byte) (CodexCache, bool, error) {
	digestChain := ClaudeCodeAnthropicDigestChain(payload)
	if digestChain == "" {
		return CodexCache{}, false, nil
	}
	if cache, ok, err := findClaudeCodeDigestPromptCache(ctx, digestChain); err != nil || ok {
		if err != nil {
			return CodexCache{}, false, err
		}
		if errSet := SetCodexCacheRequired(ctx, ClaudeCodeDigestPromptCacheKey(digestChain), cache); errSet != nil {
			return CodexCache{}, false, errSet
		}
		return cache, true, nil
	}
	cache := CodexCache{
		ID:     "anthropic-digest-" + claudeCodeDigestHash(digestChain),
		Expire: time.Now().Add(1 * time.Hour),
	}
	if errSet := SetCodexCacheRequired(ctx, ClaudeCodeDigestPromptCacheKey(digestChain), cache); errSet != nil {
		return CodexCache{}, false, errSet
	}
	return cache, true, nil
}

func findClaudeCodeDigestPromptCache(ctx context.Context, digestChain string) (CodexCache, bool, error) {
	chain := strings.TrimSpace(digestChain)
	for chain != "" {
		cache, ok, err := GetCodexCacheRequired(ctx, ClaudeCodeDigestPromptCacheKey(chain))
		if err != nil || ok {
			return cache, ok, err
		}
		i := strings.LastIndex(chain, "-")
		if i < 0 {
			return CodexCache{}, false, nil
		}
		chain = chain[:i]
	}
	return CodexCache{}, false, nil
}

func ClaudeCodeAnthropicDigestChain(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	parts := make([]string, 0, 8)
	if system := gjson.GetBytes(payload, "system"); system.Exists() {
		raw := strings.TrimSpace(system.Raw)
		if raw != "" && raw != "null" {
			parts = append(parts, "s:"+claudeCodeDigestHash(canonicalClaudeCodeDigestJSON(raw)))
		}
	}
	messages := gjson.GetBytes(payload, "messages")
	if messages.IsArray() {
		messages.ForEach(func(_, message gjson.Result) bool {
			content := message.Get("content")
			raw := strings.TrimSpace(content.Raw)
			if raw == "" || raw == "null" {
				return true
			}
			prefix := "u"
			if strings.TrimSpace(message.Get("role").String()) == "assistant" {
				prefix = "a"
			}
			parts = append(parts, prefix+":"+claudeCodeDigestHash(canonicalClaudeCodeDigestJSON(raw)))
			return true
		})
	}
	return strings.Join(parts, "-")
}

func canonicalClaudeCodeDigestJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func claudeCodeDigestHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:8])
}

// ClaudeCodeCacheControlPromptCacheKey derives a deterministic cache identity from
// Anthropic cache_control anchors when Claude Code session metadata is unavailable.
func ClaudeCodeCacheControlPromptCacheKey(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var parts []string
	system := gjson.GetBytes(payload, "system")
	if system.IsArray() {
		system.ForEach(func(_, block gjson.Result) bool {
			if text := claudeCodeCacheControlText(block); text != "" {
				parts = append(parts, "system:"+text)
			}
			return true
		})
	}

	firstUserAnchor := ""
	messages := gjson.GetBytes(payload, "messages")
	if messages.IsArray() {
		messages.ForEach(func(_, message gjson.Result) bool {
			role := strings.TrimSpace(message.Get("role").String())
			content := message.Get("content")
			if !content.IsArray() {
				return true
			}
			content.ForEach(func(_, block gjson.Result) bool {
				text := claudeCodeCacheControlText(block)
				if text == "" {
					return true
				}
				switch role {
				case "user":
					if firstUserAnchor == "" {
						firstUserAnchor = text
					}
				case "assistant":
					parts = append(parts, "assistant:"+text)
				}
				return true
			})
			return true
		})
	}
	if firstUserAnchor != "" {
		parts = append(parts, "user_anchor:"+firstUserAnchor)
	}
	if len(parts) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte("anthropic-cache:" + strings.Join(parts, "\n")))
	return fmt.Sprintf("anthropic-cache-%x", sum[:16])
}

func claudeCodeCacheControlText(block gjson.Result) string {
	if block.Get("type").String() != "text" {
		return ""
	}
	if strings.TrimSpace(block.Get("cache_control.type").String()) != "ephemeral" {
		return ""
	}
	return strings.TrimSpace(block.Get("text").String())
}
