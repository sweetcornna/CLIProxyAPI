package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/tidwall/sjson"
	"golang.org/x/net/context"
)

// apiKeyFromContext returns the inbound proxy API key bound to this request, if any.
// The auth middleware stores it on the gin context as "userApiKey".
func apiKeyFromContext(ctx context.Context) string {
	if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil {
		return strings.TrimSpace(ginCtx.GetString("userApiKey"))
	}
	return ""
}

// groupForContext resolves the account group bound to the inbound API key, or nil.
func (h *BaseAPIHandler) groupForContext(ctx context.Context) *config.GroupConfig {
	if h.Cfg == nil || len(h.Cfg.Groups) == 0 {
		return nil
	}
	return config.GroupForAPIKey(h.Cfg.Groups, apiKeyFromContext(ctx))
}

// modelMappingRulesForRequest returns the effective mapping rules for this request:
// the bound group's table when it defines one, otherwise the global table. Returns
// nil when mapping is disabled or unconfigured.
func (h *BaseAPIHandler) modelMappingRulesForRequest(ctx context.Context) []config.ModelMappingRule {
	if h.Cfg == nil {
		return nil
	}
	if grp := h.groupForContext(ctx); grp != nil && grp.ModelMapping.Configured() {
		if grp.ModelMapping.IsEnabled() {
			return grp.ModelMapping.Rules
		}
		return nil
	}
	if h.Cfg.ModelMapping.IsEnabled() {
		return h.Cfg.ModelMapping.Rules
	}
	return nil
}

// setGroupMetadata records the bound group on the request metadata so the auth
// conductor can narrow credential selection and apply any fallback group.
func (h *BaseAPIHandler) setGroupMetadata(ctx context.Context, reqMeta map[string]any) {
	if reqMeta == nil {
		return
	}
	if grp := h.groupForContext(ctx); grp != nil {
		reqMeta[coreexecutor.GroupNameMetadataKey] = grp.Name
	}
}

// rewriteMappedModel applies the configured claude→upstream model mapping for
// claude-protocol requests. It returns the possibly-rewritten model name and
// request body. It only acts when entryProtocol is "claude" and a rule matches;
// otherwise inputs are returned unchanged so OpenAI/Codex/Gemini passthrough — and
// any client that already sends an upstream model id (e.g. gpt-5.5) — is untouched.
//
// This must run before applyModelRouter/providersForExecution because provider
// resolution keys off the model name: a raw claude-* name would otherwise resolve
// to the real Claude provider (or fail) before any rewrite could take effect.
func (h *BaseAPIHandler) rewriteMappedModel(ctx context.Context, entryProtocol, modelName string, rawJSON []byte) (string, []byte) {
	if !strings.EqualFold(entryProtocol, "claude") {
		return modelName, rawJSON
	}
	rules := h.modelMappingRulesForRequest(ctx)
	if len(rules) == 0 {
		return modelName, rawJSON
	}
	target, ok := config.ResolveMappedModel(rules, modelName)
	if !ok || target == "" || strings.EqualFold(target, modelName) {
		return modelName, rawJSON
	}
	if len(rawJSON) > 0 {
		if updated, err := sjson.SetBytes(rawJSON, "model", target); err == nil {
			rawJSON = updated
		}
	}
	return target, rawJSON
}
