package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
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

func (h *BaseAPIHandler) groupBindingMissing(ctx context.Context) bool {
	if h == nil || h.Cfg == nil || len(h.Cfg.Groups) == 0 {
		return false
	}
	return apiKeyFromContext(ctx) != "" && h.groupForContext(ctx) == nil
}

func (h *BaseAPIHandler) validateGroupBindingAccess(ctx context.Context) *interfaces.ErrorMessage {
	if !h.groupBindingMissing(ctx) {
		return nil
	}
	return &interfaces.ErrorMessage{
		StatusCode: http.StatusForbidden,
		Error:      fmt.Errorf("api key is not bound to a group"),
	}
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

// ModelsForRequest narrows model discovery to the auth credentials available to
// the API-key-bound group. Groups that only configure model mapping do not narrow
// discovery; groups with providers/credentials use the same matcher as execution.
func (h *BaseAPIHandler) ModelsForRequest(c *gin.Context, models []map[string]any) []map[string]any {
	if h == nil || c == nil || len(models) == 0 {
		return models
	}
	ctx := context.WithValue(c.Request.Context(), "gin", c)
	grp := h.groupForContext(ctx)
	if h.groupBindingMissing(ctx) {
		return []map[string]any{}
	}
	if grp == nil || (!groupHasCredentialScope(grp) && !config.GroupHasModelScope(grp)) || h.AuthManager == nil {
		return models
	}

	allowed := make(map[string]struct{})
	if groupHasCredentialScope(grp) {
		for _, authEntry := range h.AuthManager.List() {
			if !coreauth.AuthMatchesGroup(authEntry, grp) {
				continue
			}
			for _, clientID := range authModelClientIDs(authEntry) {
				for _, model := range registry.GetGlobalRegistry().GetModelsForClient(clientID) {
					if model == nil || strings.TrimSpace(model.ID) == "" {
						continue
					}
					allowed[model.ID] = struct{}{}
					if model.Name != "" {
						allowed[strings.TrimPrefix(model.Name, "models/")] = struct{}{}
						allowed[model.Name] = struct{}{}
					}
				}
			}
		}
		if len(allowed) == 0 {
			return []map[string]any{}
		}
	}

	filtered := make([]map[string]any, 0, len(models))
	for _, model := range models {
		id := modelDiscoveryID(model)
		if len(allowed) > 0 {
			if _, ok := allowed[id]; !ok {
				continue
			}
		}
		if !config.GroupAllowsModel(grp, modelDiscoveryCandidates(model)...) {
			continue
		}
		filtered = append(filtered, model)
	}
	return filtered
}

func groupHasCredentialScope(grp *config.GroupConfig) bool {
	return grp != nil && (grp.DenyCredentials || len(grp.Providers) > 0 || len(grp.Credentials) > 0)
}

func authModelClientIDs(authEntry *coreauth.Auth) []string {
	if authEntry == nil {
		return nil
	}
	out := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, candidate := range []string{authEntry.ID, authEntry.FileName} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func modelDiscoveryID(model map[string]any) string {
	for _, key := range []string{"id", "slug", "name"} {
		value, _ := model[key].(string)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if key == "name" {
			value = strings.TrimPrefix(value, "models/")
		}
		return value
	}
	return ""
}

func modelDiscoveryCandidates(model map[string]any) []string {
	out := make([]string, 0, 3)
	for _, key := range []string{"id", "slug", "name"} {
		value, _ := model[key].(string)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value, strings.TrimPrefix(value, "models/"))
	}
	return out
}

func (h *BaseAPIHandler) validateGroupModelAccess(ctx context.Context, modelNames ...string) *interfaces.ErrorMessage {
	grp := h.groupForContext(ctx)
	if grp == nil || !config.GroupHasModelScope(grp) {
		return nil
	}
	candidates := expandGroupModelCandidates(modelNames...)
	if config.GroupAllowsModel(grp, candidates...) {
		return nil
	}
	requested := firstNonEmpty(modelNames...)
	if requested == "" {
		requested = "unknown"
	}
	return &interfaces.ErrorMessage{
		StatusCode: http.StatusForbidden,
		Error:      fmt.Errorf("model %s is not allowed for group %s", requested, grp.Name),
	}
}

func expandGroupModelCandidates(modelNames ...string) []string {
	out := make([]string, 0, len(modelNames)*3)
	seen := make(map[string]struct{}, len(modelNames)*3)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, candidate := range []string{value, strings.TrimPrefix(value, "models/"), thinking.ParseSuffix(value).ModelName} {
			candidate = strings.TrimSpace(candidate)
			candidate = strings.TrimPrefix(candidate, "models/")
			if candidate == "" {
				continue
			}
			key := strings.ToLower(candidate)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, candidate)
		}
	}
	for _, name := range modelNames {
		add(name)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
