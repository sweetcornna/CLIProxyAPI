package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexExecutorCacheHelper_OpenAIChatCompletions_StablePromptCacheKeyFromAPIKey(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Set("userApiKey", "test-api-key")

	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	executor := &CodexExecutor{}
	rawJSON := []byte(`{"model":"gpt-5.3-codex","stream":true}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.3-codex",
		Payload: []byte(`{"model":"gpt-5.3-codex"}`),
	}
	url := "https://example.com/responses"

	httpReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("openai"), url, nil, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}

	body, errRead := io.ReadAll(httpReq.Body)
	if errRead != nil {
		t.Fatalf("read request body: %v", errRead)
	}

	expectedKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex:prompt-cache:test-api-key")).String()
	gotKey := gjson.GetBytes(body, "prompt_cache_key").String()
	if gotKey != expectedKey {
		t.Fatalf("prompt_cache_key = %q, want %q", gotKey, expectedKey)
	}
	if gotConversation := httpReq.Header.Get("Conversation_id"); gotConversation != "" {
		t.Fatalf("Conversation_id = %q, want empty", gotConversation)
	}
	if gotSession := httpReq.Header["Session_id"]; len(gotSession) != 1 || gotSession[0] != expectedKey {
		t.Fatalf("Session_id = %#v, want [%q]", gotSession, expectedKey)
	}
	if gotCanonicalSession := httpReq.Header.Get("Session-Id"); gotCanonicalSession != "" {
		t.Fatalf("Session-Id = %q, want empty", gotCanonicalSession)
	}

	httpReq2, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("openai"), url, nil, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error (second call): %v", err)
	}
	body2, errRead2 := io.ReadAll(httpReq2.Body)
	if errRead2 != nil {
		t.Fatalf("read request body (second call): %v", errRead2)
	}
	gotKey2 := gjson.GetBytes(body2, "prompt_cache_key").String()
	if gotKey2 != expectedKey {
		t.Fatalf("prompt_cache_key (second call) = %q, want %q", gotKey2, expectedKey)
	}
}

func TestCodexExecutorCacheHelper_ClaudeUsesClaudeCodeSessionID(t *testing.T) {
	executor := &CodexExecutor{}
	ctx := context.Background()
	url := "https://example.com/responses"
	rawJSON := []byte(`{"model":"gpt-5.4","stream":true}`)
	firstReq := cliproxyexecutor.Request{
		Model: "gpt-5.4-claude-cache-session",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"cache-session-1\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]
		}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model: "gpt-5.4-claude-cache-session",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-b\",\"account_uuid\":\"\",\"session_id\":\"cache-session-1\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}

	firstHTTPReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), url, nil, firstReq, firstReq.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper first error: %v", err)
	}
	secondHTTPReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), url, nil, secondReq, secondReq.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper second error: %v", err)
	}

	firstBody, errRead := io.ReadAll(firstHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read first request body: %v", errRead)
	}
	secondBody, errRead := io.ReadAll(secondHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read second request body: %v", errRead)
	}
	firstKey := gjson.GetBytes(firstBody, "prompt_cache_key").String()
	secondKey := gjson.GetBytes(secondBody, "prompt_cache_key").String()
	if firstKey == "" {
		t.Fatalf("first prompt_cache_key is empty; body=%s", string(firstBody))
	}
	if secondKey != firstKey {
		t.Fatalf("same Claude Code session_id produced different prompt_cache_key: first=%q second=%q", firstKey, secondKey)
	}
	if gotSession := firstHTTPReq.Header["Session_id"]; len(gotSession) != 1 || gotSession[0] != firstKey {
		t.Fatalf("first Session_id = %#v, want [%q]", gotSession, firstKey)
	}
	if gotSession := secondHTTPReq.Header["Session_id"]; len(gotSession) != 1 || gotSession[0] != firstKey {
		t.Fatalf("second Session_id = %#v, want [%q]", gotSession, firstKey)
	}
}

func TestCodexExecutorCacheHelper_ClaudeBareUserIDUsesDigestFallback(t *testing.T) {
	executor := &CodexExecutor{}
	firstReq := cliproxyexecutor.Request{
		Model:   "gpt-5.4-claude-cache-bare-user",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"same-user-across-chats"},"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model:   "gpt-5.4-claude-cache-bare-user",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"different-user-same-chat-shape"},"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]}`),
	}

	firstHTTPReq, _, _, err := executor.cacheHelper(context.Background(), sdktranslator.FromString("claude"), "https://example.com/responses", nil, firstReq, firstReq.Payload, []byte(`{"model":"gpt-5.4","stream":true}`))
	if err != nil {
		t.Fatalf("cacheHelper first error: %v", err)
	}
	secondHTTPReq, _, _, err := executor.cacheHelper(context.Background(), sdktranslator.FromString("claude"), "https://example.com/responses", nil, secondReq, secondReq.Payload, []byte(`{"model":"gpt-5.4","stream":true}`))
	if err != nil {
		t.Fatalf("cacheHelper second error: %v", err)
	}

	firstBody, errRead := io.ReadAll(firstHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read first request body: %v", errRead)
	}
	secondBody, errRead := io.ReadAll(secondHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read second request body: %v", errRead)
	}
	firstKey := gjson.GetBytes(firstBody, "prompt_cache_key").String()
	secondKey := gjson.GetBytes(secondBody, "prompt_cache_key").String()
	if !strings.HasPrefix(firstKey, "anthropic-digest-") {
		t.Fatalf("prompt_cache_key = %q, want anthropic-digest; body=%s", firstKey, string(firstBody))
	}
	if secondKey != firstKey {
		t.Fatalf("bare metadata.user_id changed digest prompt_cache_key to %q, want %q", secondKey, firstKey)
	}
	if got := firstHTTPReq.Header["Session_id"]; len(got) != 1 || got[0] != firstKey {
		t.Fatalf("first Session_id = %#v, want [%q]", got, firstKey)
	}
	if got := secondHTTPReq.Header["Session_id"]; len(got) != 1 || got[0] != firstKey {
		t.Fatalf("second Session_id = %#v, want [%q]", got, firstKey)
	}
}

func TestCodexExecutorCacheHelper_IdentityConfuseRemapsBodyAndHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ginCtx.Request.Header.Set("X-Codex-Turn-Metadata", `{"prompt_cache_key":"cache-1","turn_id":"turn-1","window_id":"cache-1:0"}`)
	ginCtx.Request.Header.Set("X-Client-Request-Id", "client-request-1")

	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	executor := &CodexExecutor{cfg: &config.Config{
		Routing: config.RoutingConfig{Strategy: "fill-first"},
		Codex:   config.CodexConfig{IdentityConfuse: true},
	}}
	auth := &cliproxyauth.Auth{ID: "auth-1", Provider: "codex"}
	rawJSON := []byte(`{"model":"gpt-5-codex","stream":true,"client_metadata":{"x-codex-turn-metadata":"{\"prompt_cache_key\":\"cache-1\",\"turn_id\":\"turn-1\",\"window_id\":\"cache-1:0\"}","x-codex-window-id":"cache-1:0"}}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","prompt_cache_key":"cache-1","client_metadata":{"x-codex-installation-id":"install-1"}}`),
	}
	url := "https://example.com/responses"

	httpReq, body, identityState, err := executor.cacheHelper(ctx, sdktranslator.FromString("openai-response"), url, auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)

	expectedPromptCacheKey := codexIdentityConfuseUUID("auth-1", "prompt-cache", "cache-1")
	expectedTurnID := codexIdentityConfuseUUID("auth-1", "turn", "turn-1")
	if gotKey := gjson.GetBytes(body, "prompt_cache_key").String(); gotKey != expectedPromptCacheKey {
		t.Fatalf("prompt_cache_key = %q, want %q", gotKey, expectedPromptCacheKey)
	}
	expectedInstallationID := codexIdentityConfuseUUID("auth-1", "installation", "install-1")
	if gotID := gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String(); gotID != expectedInstallationID {
		t.Fatalf("installation id = %q, want %q", gotID, expectedInstallationID)
	}
	gotBodyMetadata := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()
	if gotMetadataPromptCacheKey := gjson.Get(gotBodyMetadata, "prompt_cache_key").String(); gotMetadataPromptCacheKey != expectedPromptCacheKey {
		t.Fatalf("client_metadata.x-codex-turn-metadata.prompt_cache_key = %q, want %q", gotMetadataPromptCacheKey, expectedPromptCacheKey)
	}
	if gotMetadataTurnID := gjson.Get(gotBodyMetadata, "turn_id").String(); gotMetadataTurnID != expectedTurnID {
		t.Fatalf("client_metadata.x-codex-turn-metadata.turn_id = %q, want %q", gotMetadataTurnID, expectedTurnID)
	}
	if gotMetadataWindowID := gjson.Get(gotBodyMetadata, "window_id").String(); gotMetadataWindowID != expectedPromptCacheKey+":0" {
		t.Fatalf("client_metadata.x-codex-turn-metadata.window_id = %q, want %q", gotMetadataWindowID, expectedPromptCacheKey+":0")
	}
	if gotWindowID := gjson.GetBytes(body, "client_metadata.x-codex-window-id").String(); gotWindowID != expectedPromptCacheKey+":0" {
		t.Fatalf("client_metadata.x-codex-window-id = %q, want %q", gotWindowID, expectedPromptCacheKey+":0")
	}
	if gotHeader := httpReq.Header["Session_id"]; len(gotHeader) != 1 || gotHeader[0] != expectedPromptCacheKey {
		t.Fatalf("Session_id = %#v, want [%q]", gotHeader, expectedPromptCacheKey)
	}
	for _, headerName := range []string{"X-Client-Request-Id", "Thread-Id"} {
		if gotHeader := httpReq.Header.Get(headerName); gotHeader != expectedPromptCacheKey {
			t.Fatalf("%s = %q, want %q", headerName, gotHeader, expectedPromptCacheKey)
		}
	}
	if gotCanonicalSession := httpReq.Header.Get("Session-Id"); gotCanonicalSession != "" {
		t.Fatalf("Session-Id = %q, want empty", gotCanonicalSession)
	}
	if gotWindow := httpReq.Header.Get("X-Codex-Window-Id"); gotWindow != expectedPromptCacheKey+":0" {
		t.Fatalf("X-Codex-Window-Id = %q, want %q", gotWindow, expectedPromptCacheKey+":0")
	}
	gotHeaderMetadata := httpReq.Header.Get("X-Codex-Turn-Metadata")
	if gotMetadataPromptCacheKey := gjson.Get(gotHeaderMetadata, "prompt_cache_key").String(); gotMetadataPromptCacheKey != expectedPromptCacheKey {
		t.Fatalf("X-Codex-Turn-Metadata.prompt_cache_key = %q, want %q", gotMetadataPromptCacheKey, expectedPromptCacheKey)
	}
	if gotMetadataTurnID := gjson.Get(gotHeaderMetadata, "turn_id").String(); gotMetadataTurnID != expectedTurnID {
		t.Fatalf("X-Codex-Turn-Metadata.turn_id = %q, want %q", gotMetadataTurnID, expectedTurnID)
	}
	if gotMetadataWindowID := gjson.Get(gotHeaderMetadata, "window_id").String(); gotMetadataWindowID != expectedPromptCacheKey+":0" {
		t.Fatalf("X-Codex-Turn-Metadata.window_id = %q, want %q", gotMetadataWindowID, expectedPromptCacheKey+":0")
	}
}

func TestCodexExecutorCacheHelper_TrimsInputWhenPreviousResponseIDIsAttached(t *testing.T) {
	ctx := context.Background()
	executor := &CodexExecutor{}
	auth := &cliproxyauth.Auth{
		ID: "auth-trim-continuation",
		Attributes: map[string]string{
			"api_key": "test",
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","prompt_cache_key":"trim-cache"}`),
	}
	rawJSON := []byte(`{
		"model":"gpt-5.4",
		"prompt_cache_key":"trim-cache",
		"input":[
			{"type":"message","role":"user","content":"old"},
			{"type":"message","role":"assistant","content":"old answer"},
			{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{}"},
			{"type":"function_call_output","call_id":"call-1","output":"ok"},
			{"type":"message","role":"user","content":"next"}
		]
	}`)
	if !helps.SetCodexPreviousResponseIDBestEffort(ctx, codexPreviousResponseAuthScope(auth), "trim-cache", "resp_previous") {
		t.Fatal("failed to seed previous response id")
	}

	httpReq, body, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("openai-response"), "https://example.com/responses", auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if httpReq == nil {
		t.Fatal("http request is nil")
	}

	if got := gjson.GetBytes(body, "previous_response_id").String(); got != "resp_previous" {
		t.Fatalf("previous_response_id = %q, want resp_previous; body=%s", got, body)
	}
	if got := len(gjson.GetBytes(body, "input").Array()); got != 3 {
		t.Fatalf("trimmed input length = %d, want 3; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.0.type").String(); got != "function_call" {
		t.Fatalf("input.0.type = %q, want function_call; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.1.type").String(); got != "function_call_output" {
		t.Fatalf("input.1.type = %q, want function_call_output; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.2.role").String(); got != "user" {
		t.Fatalf("input.2.role = %q, want user; body=%s", got, body)
	}
}

func TestCodexExecutorCacheHelper_ClaudeAPIKeyTrimsFullReplayAndAddsTodoGuard(t *testing.T) {
	ctx := context.Background()
	executor := &CodexExecutor{}
	auth := &cliproxyauth.Auth{
		ID: "auth-full-replay-guard",
		Attributes: map[string]string{
			"api_key": "test",
		},
	}
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"full-replay-guard-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"message-00"}]}]
		}`),
	}
	rawJSON := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-00"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-01"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-02"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-03"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-04"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-05"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-06"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-07"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-08"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-09"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-10"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-11"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-12"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-13"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-14"}]}
		]
	}`)

	httpReq, body, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), "https://example.com/responses", auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if httpReq == nil {
		t.Fatal("http request is nil")
	}

	if got := len(gjson.GetBytes(body, "input").Array()); got != 13 {
		t.Fatalf("input length = %d, want 13 (developer guard + 12 tail messages); body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.0.role").String(); got != "developer" {
		t.Fatalf("input.0.role = %q, want developer; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.0.content.0.text").String(); !strings.Contains(got, openAICompatClaudeCodeTodoGuardMarker) {
		t.Fatalf("developer guard missing marker; text=%q body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.1.content.0.text").String(); got != "message-03" {
		t.Fatalf("first retained message = %q, want message-03; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.12.content.0.text").String(); got != "message-14" {
		t.Fatalf("last retained message = %q, want message-14; body=%s", got, body)
	}
}

func TestCodexExecutorCacheHelper_ClaudeOAuthKeepsFullReplayAndAddsTodoGuard(t *testing.T) {
	ctx := context.Background()
	executor := &CodexExecutor{}
	auth := &cliproxyauth.Auth{
		ID:       "auth-oauth-full-replay-guard",
		Metadata: map[string]any{"access_token": "oauth-token"},
	}
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"oauth-full-replay-guard-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"message-00"}]}]
		}`),
	}
	rawJSON := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-00"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-01"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-02"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-03"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-04"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-05"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-06"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-07"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-08"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-09"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-10"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-11"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-12"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-13"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"message-14"}]}
		]
	}`)

	httpReq, body, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), "https://example.com/responses", auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if httpReq == nil {
		t.Fatal("http request is nil")
	}

	if got := len(gjson.GetBytes(body, "input").Array()); got != 16 {
		t.Fatalf("input length = %d, want 16 (developer guard + full replay); body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.0.role").String(); got != "developer" {
		t.Fatalf("input.0.role = %q, want developer; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.0.content.0.text").String(); !strings.Contains(got, openAICompatClaudeCodeTodoGuardMarker) {
		t.Fatalf("developer guard missing marker; text=%q body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.1.content.0.text").String(); got != "message-00" {
		t.Fatalf("first retained message = %q, want message-00; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.15.content.0.text").String(); got != "message-14" {
		t.Fatalf("last retained message = %q, want message-14; body=%s", got, body)
	}
}

func TestApplyCodexHeadersUsesAccountHeaderForOAuth(t *testing.T) {
	httpReq := httptest.NewRequest("POST", "https://example.com/responses", nil)
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"account_id": "acct-1"},
	}

	applyCodexHeaders(httpReq, auth, "oauth-token", true, nil)

	if got := httpReq.Header.Get("Chatgpt-Account-Id"); got != "acct-1" {
		t.Fatalf("Chatgpt-Account-Id = %q, want acct-1", got)
	}
}

func TestCodexIdentityConfuseKeepsClientBodySeparateFromUpstreamBody(t *testing.T) {
	cfg := &config.Config{
		Routing: config.RoutingConfig{Strategy: "fill-first"},
		Codex:   config.CodexConfig{IdentityConfuse: true},
	}
	auth := &cliproxyauth.Auth{ID: "auth-1", Provider: "codex"}
	clientBody := []byte(`{"model":"gpt-5-codex","prompt_cache_key":"cache-1"}`)

	upstreamBody, identityState := applyCodexIdentityConfuseBody(cfg, auth, clientBody, clientBody)
	expectedPromptCacheKey := codexIdentityConfuseUUID("auth-1", "prompt-cache", "cache-1")
	if identityState.promptCacheKey != expectedPromptCacheKey {
		t.Fatalf("identity prompt_cache_key = %q, want %q", identityState.promptCacheKey, expectedPromptCacheKey)
	}
	if gotKey := gjson.GetBytes(upstreamBody, "prompt_cache_key").String(); gotKey != expectedPromptCacheKey {
		t.Fatalf("upstream prompt_cache_key = %q, want %q", gotKey, expectedPromptCacheKey)
	}
	if gotKey := gjson.GetBytes(clientBody, "prompt_cache_key").String(); gotKey != "cache-1" {
		t.Fatalf("client prompt_cache_key = %q, want cache-1", gotKey)
	}
}

func TestCodexExecutorCacheHelper_ClaudeUsesSessionHeader(t *testing.T) {
	executor := &CodexExecutor{}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ginCtx.Request.Header.Set(helps.ClaudeCodeSessionHeader, "cache-session-header")
	ctx := context.WithValue(context.Background(), "gin", ginCtx)

	firstReq := cliproxyexecutor.Request{
		Model:   "gpt-5.4-claude-cache-header",
		Payload: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model:   "gpt-5.4-claude-cache-header",
		Payload: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}
	rawJSON := []byte(`{"model":"gpt-5.4","stream":true}`)
	url := "https://example.com/responses"

	firstHTTPReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), url, nil, firstReq, firstReq.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper first error: %v", err)
	}
	secondHTTPReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), url, nil, secondReq, secondReq.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper second error: %v", err)
	}

	firstBody, errRead := io.ReadAll(firstHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read first request body: %v", errRead)
	}
	secondBody, errRead := io.ReadAll(secondHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read second request body: %v", errRead)
	}
	firstKey := gjson.GetBytes(firstBody, "prompt_cache_key").String()
	secondKey := gjson.GetBytes(secondBody, "prompt_cache_key").String()
	if firstKey == "" {
		t.Fatalf("first prompt_cache_key is empty; body=%s", string(firstBody))
	}
	if secondKey != firstKey {
		t.Fatalf("same Claude Code session header produced different prompt_cache_key: first=%q second=%q", firstKey, secondKey)
	}
}

func TestCodexExecutorCacheHelper_ClaudeSessionStableAcrossModels(t *testing.T) {
	executor := &CodexExecutor{}
	ctx := context.Background()
	url := "https://example.com/responses"
	firstReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"cache-session-cross-model\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]
		}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model: "gpt-5.5",
		Payload: []byte(`{
			"model":"gpt-5.5",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"cache-session-cross-model\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}

	firstHTTPReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), url, nil, firstReq, firstReq.Payload, []byte(`{"model":"gpt-5.4","stream":true}`))
	if err != nil {
		t.Fatalf("cacheHelper first error: %v", err)
	}
	secondHTTPReq, _, _, err := executor.cacheHelper(ctx, sdktranslator.FromString("claude"), url, nil, secondReq, secondReq.Payload, []byte(`{"model":"gpt-5.5","stream":true}`))
	if err != nil {
		t.Fatalf("cacheHelper second error: %v", err)
	}

	firstBody, errRead := io.ReadAll(firstHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read first request body: %v", errRead)
	}
	secondBody, errRead := io.ReadAll(secondHTTPReq.Body)
	if errRead != nil {
		t.Fatalf("read second request body: %v", errRead)
	}
	firstKey := gjson.GetBytes(firstBody, "prompt_cache_key").String()
	secondKey := gjson.GetBytes(secondBody, "prompt_cache_key").String()
	if firstKey == "" {
		t.Fatalf("first prompt_cache_key is empty; body=%s", string(firstBody))
	}
	if secondKey != firstKey {
		t.Fatalf("same Claude Code session across models produced different prompt_cache_key: first=%q second=%q", firstKey, secondKey)
	}
}

func TestCodexExecutorExecuteRemembersTurnStateForClaudeCodeSession(t *testing.T) {
	var requests []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Header.Clone())
		if len(requests) == 1 {
			w.Header().Set("X-Codex-Turn-State", "turn-state-1")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL,
		"api_key":  "test",
	}}
	firstReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"turn-state-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]
		}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"turn-state-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	if _, err := executor.Execute(context.Background(), auth, firstReq, opts); err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if _, err := executor.Execute(context.Background(), auth, secondReq, opts); err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if got := requests[0].Get("X-Codex-Turn-State"); got != "" {
		t.Fatalf("first request turn state = %q, want empty", got)
	}
	if got := requests[1].Get("X-Codex-Turn-State"); got != "turn-state-1" {
		t.Fatalf("second request turn state = %q, want turn-state-1", got)
	}
}

func TestCodexExecutorExecuteAttachesPreviousResponseIDForAPIKeyClaudeCodeSession(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read request body: %v", errRead)
		}
		bodies = append(bodies, body)
		responseID := "resp_first"
		if len(bodies) == 2 {
			responseID = "resp_second"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"` + responseID + `","model":"gpt-5.4","status":"completed","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-api-key-continuation",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}
	firstReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]
		}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	if _, err := executor.Execute(context.Background(), auth, firstReq, opts); err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if _, err := executor.Execute(context.Background(), auth, secondReq, opts); err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if got := gjson.GetBytes(bodies[0], "previous_response_id").String(); got != "" {
		t.Fatalf("first previous_response_id = %q, want empty; body=%s", got, bodies[0])
	}
	if got := gjson.GetBytes(bodies[1], "previous_response_id").String(); got != "resp_first" {
		t.Fatalf("second previous_response_id = %q, want resp_first; body=%s", got, bodies[1])
	}
}

func TestCodexExecutorExecuteDoesNotAttachPreviousResponseIDForOAuth(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read request body: %v", errRead)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_oauth","model":"gpt-5.4","status":"completed","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:         "auth-oauth-no-continuation",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "oauth-token"},
	}
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"oauth-previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
		}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	if _, err := executor.Execute(context.Background(), auth, req, opts); err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if _, err := executor.Execute(context.Background(), auth, req, opts); err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if got := gjson.GetBytes(bodies[0], "previous_response_id").String(); got != "" {
		t.Fatalf("first previous_response_id = %q, want empty; body=%s", got, bodies[0])
	}
	if got := gjson.GetBytes(bodies[1], "previous_response_id").String(); got != "" {
		t.Fatalf("second previous_response_id = %q, want empty; body=%s", got, bodies[1])
	}
}

func TestCodexExecutorExecuteRetriesWithoutPreviousResponseIDWhenMissing(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read request body: %v", errRead)
		}
		bodies = append(bodies, body)
		if len(bodies) == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"No response found for previous_response_id resp_first","type":"invalid_request_error","code":"previous_response_not_found"}}`))
			return
		}
		responseID := "resp_first"
		if len(bodies) == 3 {
			responseID = "resp_replayed"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"` + responseID + `","model":"gpt-5.4","status":"completed","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-api-key-missing-continuation",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}
	firstReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"missing-previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]
		}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"missing-previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	if _, err := executor.Execute(context.Background(), auth, firstReq, opts); err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if _, err := executor.Execute(context.Background(), auth, secondReq, opts); err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 3 {
		t.Fatalf("request count = %d, want 3", len(bodies))
	}
	if got := gjson.GetBytes(bodies[1], "previous_response_id").String(); got != "resp_first" {
		t.Fatalf("stale attempt previous_response_id = %q, want resp_first; body=%s", got, bodies[1])
	}
	if got := gjson.GetBytes(bodies[2], "previous_response_id").String(); got != "" {
		t.Fatalf("replay previous_response_id = %q, want empty; body=%s", got, bodies[2])
	}
}

func TestCodexExecutorExecuteDisablesPreviousResponseIDWhenUnsupported(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read request body: %v", errRead)
		}
		bodies = append(bodies, body)
		if len(bodies) == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"previous_response_id is only supported on Responses WebSocket v2","type":"invalid_request_error","code":"invalid_request_error"}}`))
			return
		}
		responseID := "resp_first"
		if len(bodies) >= 3 {
			responseID = "resp_replayed"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"` + responseID + `","model":"gpt-5.4","status":"completed","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-api-key-unsupported-continuation",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}
	firstReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"unsupported-previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"first"}]}]
		}`),
	}
	secondReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"unsupported-previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}
	thirdReq := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"unsupported-previous-response-session\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"after-disable"}]}]
		}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	if _, err := executor.Execute(context.Background(), auth, firstReq, opts); err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if _, err := executor.Execute(context.Background(), auth, secondReq, opts); err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if _, err := executor.Execute(context.Background(), auth, thirdReq, opts); err != nil {
		t.Fatalf("third Execute error: %v", err)
	}

	if len(bodies) != 4 {
		t.Fatalf("request count = %d, want 4", len(bodies))
	}
	if got := gjson.GetBytes(bodies[1], "previous_response_id").String(); got != "resp_first" {
		t.Fatalf("unsupported attempt previous_response_id = %q, want resp_first; body=%s", got, bodies[1])
	}
	if got := gjson.GetBytes(bodies[2], "previous_response_id").String(); got != "" {
		t.Fatalf("unsupported replay previous_response_id = %q, want empty; body=%s", got, bodies[2])
	}
	if got := gjson.GetBytes(bodies[3], "previous_response_id").String(); got != "" {
		t.Fatalf("disabled future previous_response_id = %q, want empty; body=%s", got, bodies[3])
	}
}

func TestCodexExecutorExecutePreservesClientTurnState(t *testing.T) {
	var gotTurnState string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTurnState = r.Header.Get("X-Codex-Turn-State")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ginCtx.Request.Header.Set("X-Codex-Turn-State", "client-turn-state")
	ctx := context.WithValue(context.Background(), "gin", ginCtx)

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL,
		"api_key":  "test",
	}}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","input":"hello","prompt_cache_key":"client-cache"}`),
	}

	if _, err := executor.Execute(ctx, auth, req, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       false,
	}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if gotTurnState != "client-turn-state" {
		t.Fatalf("upstream X-Codex-Turn-State = %q, want client-turn-state", gotTurnState)
	}
}
