package helps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExtractClaudeCodeSessionIDFromPayloadJSON(t *testing.T) {
	payload := []byte(`{"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"cache-session-1\"}"}}`)
	got := ExtractClaudeCodeSessionID(context.Background(), payload, nil)
	if got != "cache-session-1" {
		t.Fatalf("ExtractClaudeCodeSessionID() = %q, want cache-session-1", got)
	}
}

func TestExtractClaudeCodeSessionIDFromLegacyPayloadUppercase(t *testing.T) {
	payload := []byte(`{"metadata":{"user_id":"user_A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2_account__session_123E4567-E89B-12D3-A456-426614174000"}}`)
	got := ExtractClaudeCodeSessionID(context.Background(), payload, nil)
	if got != "123E4567-E89B-12D3-A456-426614174000" {
		t.Fatalf("ExtractClaudeCodeSessionID() = %q, want legacy session id", got)
	}
}

func TestExtractClaudeCodeSessionIDFromHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ginCtx.Request.Header.Set(ClaudeCodeSessionHeader, "header-session-1")
	ctx := context.WithValue(context.Background(), "gin", ginCtx)

	got := ExtractClaudeCodeSessionID(ctx, []byte(`{"model":"gpt-5.4"}`), nil)
	if got != "header-session-1" {
		t.Fatalf("ExtractClaudeCodeSessionID() = %q, want header-session-1", got)
	}
}

func TestClaudeCodePromptCacheStableAcrossRequests(t *testing.T) {
	ctx := context.Background()
	payload := []byte(`{"metadata":{"user_id":"{\"session_id\":\"cache-session-2\"}"}}`)
	first, ok, err := ClaudeCodePromptCache(ctx, "grok-composer-2.5-fast", payload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache first error: %v", err)
	}
	if !ok || first.ID == "" {
		t.Fatalf("ClaudeCodePromptCache first = %#v, ok=%v, want cached id", first, ok)
	}
	second, ok, err := ClaudeCodePromptCache(ctx, "grok-composer-2.5-fast", payload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache second error: %v", err)
	}
	if !ok || second.ID != first.ID {
		t.Fatalf("second cache id = %q, want %q", second.ID, first.ID)
	}
}

func TestClaudeCodePromptCacheStableAcrossModels(t *testing.T) {
	ctx := context.Background()
	payload := []byte(`{"metadata":{"user_id":"{\"session_id\":\"cache-session-cross-model\"}"}}`)

	first, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", payload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache first error: %v", err)
	}
	if !ok || first.ID == "" {
		t.Fatalf("ClaudeCodePromptCache first = %#v, ok=%v, want cached id", first, ok)
	}
	second, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.5", payload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache second error: %v", err)
	}
	if !ok || second.ID != first.ID {
		t.Fatalf("same Claude Code session across models got cache id %q, want %q", second.ID, first.ID)
	}
}

func TestClaudeCodePromptCacheStableAcrossLocalCacheReset(t *testing.T) {
	ctx := context.Background()
	payload := []byte(`{"metadata":{"user_id":"{\"session_id\":\"cache-session-reset\"}"}}`)

	first, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", payload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache first error: %v", err)
	}
	if !ok || first.ID == "" {
		t.Fatalf("ClaudeCodePromptCache first = %#v, ok=%v, want cached id", first, ok)
	}

	codexCacheMu.Lock()
	for key := range codexCacheMap {
		delete(codexCacheMap, key)
	}
	codexCacheMu.Unlock()

	second, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", payload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache second error: %v", err)
	}
	if !ok || second.ID != first.ID {
		t.Fatalf("same Claude Code session after cache reset got cache id %q, want %q", second.ID, first.ID)
	}
}

func TestClaudeCodePromptCacheFallsBackToCacheControlAnchor(t *testing.T) {
	ctx := context.Background()
	firstPayload := []byte(`{
		"system":[{"type":"text","text":"shared project instructions","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"first task"}]}]
	}`)
	secondPayload := []byte(`{
		"system":[{"type":"text","text":"shared project instructions","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first task"}]},
			{"role":"assistant","content":[{"type":"text","text":"ok"}]},
			{"role":"user","content":[{"type":"text","text":"continue"}]}
		]
	}`)

	first, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", firstPayload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache first error: %v", err)
	}
	if !ok || !strings.HasPrefix(first.ID, "anthropic-cache-") {
		t.Fatalf("ClaudeCodePromptCache first = %#v, ok=%v, want anthropic-cache key", first, ok)
	}
	second, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.5", secondPayload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache second error: %v", err)
	}
	if !ok || second.ID != first.ID {
		t.Fatalf("same cache_control anchor across models got cache id %q, want %q", second.ID, first.ID)
	}
}

func TestClaudeCodePromptCacheDigestFallbackReusesPriorTurnWithoutSessionOrAnchor(t *testing.T) {
	ctx := context.Background()
	firstPayload := []byte(`{
		"system":[{"type":"text","text":"project instructions"}],
		"messages":[{"role":"user","content":[{"type":"text","text":"first task"}]}]
	}`)
	secondPayload := []byte(`{
		"system":[{"type":"text","text":"project instructions"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first task"}]},
			{"role":"assistant","content":[{"type":"text","text":"ok"}]},
			{"role":"user","content":[{"type":"text","text":"continue"}]}
		]
	}`)

	first, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", firstPayload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache first error: %v", err)
	}
	if !ok || !strings.HasPrefix(first.ID, "anthropic-digest-") {
		t.Fatalf("ClaudeCodePromptCache first = %#v, ok=%v, want anthropic-digest key", first, ok)
	}
	second, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.5", secondPayload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache second error: %v", err)
	}
	if !ok || second.ID != first.ID {
		t.Fatalf("digest chain extension got cache id %q, want prior id %q", second.ID, first.ID)
	}
}

func TestClaudeCodePromptCacheDigestFallbackIgnoresBareMetadataUserID(t *testing.T) {
	ctx := context.Background()
	firstPayload := []byte(`{
		"metadata":{"user_id":"same-user-across-chats"},
		"messages":[{"role":"user","content":[{"type":"text","text":"first task"}]}]
	}`)
	secondPayload := []byte(`{
		"metadata":{"user_id":"different-user-same-chat-shape"},
		"messages":[{"role":"user","content":[{"type":"text","text":"first task"}]}]
	}`)

	first, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", firstPayload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache first error: %v", err)
	}
	if !ok || !strings.HasPrefix(first.ID, "anthropic-digest-") {
		t.Fatalf("ClaudeCodePromptCache first = %#v, ok=%v, want anthropic-digest key", first, ok)
	}
	second, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", secondPayload, nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache second error: %v", err)
	}
	if !ok || second.ID != first.ID {
		t.Fatalf("bare metadata.user_id changed cache id to %q, want %q", second.ID, first.ID)
	}

	metadataOnly, ok, err := ClaudeCodePromptCache(ctx, "gpt-5.4", []byte(`{
		"metadata":{"user_id":"same-user-across-chats"}
	}`), nil)
	if err != nil {
		t.Fatalf("ClaudeCodePromptCache metadata-only error: %v", err)
	}
	if ok || metadataOnly.ID != "" {
		t.Fatalf("metadata-only ClaudeCodePromptCache = %#v, ok=%v, want no digest cache without messages", metadataOnly, ok)
	}
}

func TestExtractClaudeCodeSessionIDPrefersHeaderOverPayload(t *testing.T) {
	payload := []byte(`{"metadata":{"user_id":"{"session_id":"payload-session"}"}}`)
	headers := http.Header{}
	headers.Set(ClaudeCodeSessionHeader, "header-session")

	got := ExtractClaudeCodeSessionID(context.Background(), payload, headers)
	if got != "header-session" {
		t.Fatalf("ExtractClaudeCodeSessionID() = %q, want header-session", got)
	}
}
