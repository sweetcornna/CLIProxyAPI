// Package config — claude→upstream model mapping and lightweight account grouping.
//
// These features power the Claude Code ↔ GPT compatibility fork: a request-time
// model rewrite table (e.g. claude-sonnet-* → gpt-5.5) and config-only account
// groups that bind inbound API keys to a credential selection + per-group mapping.
// They are intentionally additive and inert unless configured, so existing
// flat-credential / oauth-model-alias deployments behave exactly as before.
package config

import (
	"path/filepath"
	"sort"
	"strings"
)

// ModelMappingConfig is a request-time model rewrite table. Incoming (client)
// model names are matched against Rules and rewritten to the matched upstream
// Target before provider resolution. It is used both globally (top-level
// model-mapping) and per-group (groups[].model-mapping).
type ModelMappingConfig struct {
	// Enabled toggles the mapping layer. When nil, mapping is considered enabled
	// if (and only if) Rules is non-empty. Set explicitly to false to ship rules
	// while keeping them inert, or to disable an inherited table for a group.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// Rules is the ordered match table. After SanitizeModelMapping it is sorted
	// by descending pattern length so the longest match wins.
	Rules []ModelMappingRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// ModelMappingRule maps an incoming model name (exact id or trailing-"*" wildcard)
// to an upstream model id, with an optional advertised context window.
type ModelMappingRule struct {
	// Pattern is an exact model id (e.g. "claude-sonnet-4-5-20250929") or a
	// trailing-wildcard prefix (e.g. "claude-opus-*"). Matched case-insensitively.
	Pattern string `yaml:"pattern" json:"pattern"`
	// Target is the upstream model id the request is rewritten to (e.g. "gpt-5.5").
	Target string `yaml:"target" json:"target"`
	// ContextLength, when > 0, advertises this window (in tokens) for Target in
	// /v1/models. Used to keep Claude Code auto-compact math honest. See Feature 2.
	ContextLength int `yaml:"context-length,omitempty" json:"context-length,omitempty"`
}

// Configured reports whether the table has any usable rules at all (regardless of
// the Enabled toggle). Used to decide whether a group overrides the global table.
func (m ModelMappingConfig) Configured() bool { return len(m.Rules) > 0 }

// IsEnabled reports whether the mapping layer should be applied. A nil Enabled
// defaults to true when rules are present, false otherwise.
func (m ModelMappingConfig) IsEnabled() bool {
	if m.Enabled != nil {
		return *m.Enabled
	}
	return len(m.Rules) > 0
}

// ResolveMappedModel returns the upstream target for name. Exact (case-insensitive)
// matches win over wildcards; among wildcards the longest pattern prefix wins.
// Rules are expected to be pre-sorted by descending pattern length (see
// SanitizeModelMapping) but the function does not rely on ordering for correctness.
func ResolveMappedModel(rules []ModelMappingRule, name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || len(rules) == 0 {
		return "", false
	}
	lower := strings.ToLower(trimmed)

	// Exact match first.
	for i := range rules {
		p := rules[i].Pattern
		if !strings.HasSuffix(p, "*") && strings.EqualFold(p, lower) {
			return rules[i].Target, true
		}
	}

	// Longest trailing-wildcard prefix match.
	best := -1
	bestLen := -1
	for i := range rules {
		p := rules[i].Pattern
		if !strings.HasSuffix(p, "*") {
			continue
		}
		prefix := strings.ToLower(strings.TrimSuffix(p, "*"))
		if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			best = i
		}
	}
	if best >= 0 {
		return rules[best].Target, true
	}
	return "", false
}

// sanitizeRules trims, lowercases patterns, drops invalid/duplicate entries, and
// sorts by descending pattern length so longest-wins matching is precomputed.
func sanitizeRules(rules []ModelMappingRule) []ModelMappingRule {
	clean := make([]ModelMappingRule, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		pattern := strings.ToLower(strings.TrimSpace(r.Pattern))
		target := strings.TrimSpace(r.Target)
		if pattern == "" || target == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		clean = append(clean, ModelMappingRule{Pattern: pattern, Target: target, ContextLength: r.ContextLength})
	}
	sort.SliceStable(clean, func(i, j int) bool {
		return len(clean[i].Pattern) > len(clean[j].Pattern)
	})
	return clean
}

// GroupConfig binds a set of inbound API keys to a credential selection, an
// optional per-group model-mapping override, a routing strategy, and an optional
// fallback group. It is config-only: no database, admin UI, billing, or quotas.
type GroupConfig struct {
	// Name uniquely identifies the group (referenced by fallback-group).
	Name string `yaml:"name" json:"name"`
	// APIKeys lists inbound proxy keys bound to this group.
	APIKeys []string `yaml:"api-keys" json:"api-keys"`
	// Providers optionally restricts the group to upstream provider types (e.g. "codex").
	Providers []string `yaml:"providers,omitempty" json:"providers,omitempty"`
	// Credentials optionally restricts the group to auths whose label/id matches
	// one of these glob patterns (e.g. "codex-pro-*").
	Credentials []string `yaml:"credentials,omitempty" json:"credentials,omitempty"`
	// DenyCredentials makes the group match no upstream credentials. This is
	// useful for preserving an inbound api-key group while its allowed credential
	// set is intentionally empty; a group with no providers/credentials still
	// means "no credential narrowing" for backward compatibility.
	DenyCredentials bool `yaml:"deny-credentials,omitempty" json:"deny-credentials,omitempty"`
	// Models optionally restricts the group to requested model ids or glob
	// patterns (filepath.Match syntax). A group with no models keeps legacy
	// "all models" behavior unless DenyModels is true.
	Models []string `yaml:"models,omitempty" json:"models,omitempty"`
	// DenyModels makes the group match no requested models. This preserves an
	// inbound api-key group while its per-model allowed set is intentionally empty.
	DenyModels bool `yaml:"deny-models,omitempty" json:"deny-models,omitempty"`
	// Routing selects the per-group credential strategy (round-robin/fill-first).
	Routing RoutingConfig `yaml:"routing,omitempty" json:"routing,omitempty"`
	// ModelMapping overrides/extends the global mapping for this group.
	ModelMapping ModelMappingConfig `yaml:"model-mapping,omitempty" json:"model-mapping,omitempty"`
	// FallbackGroup is attempted (once per hop) when this group's credentials are exhausted.
	FallbackGroup string `yaml:"fallback-group,omitempty" json:"fallback-group,omitempty"`
	// FallbackGroupOnInvalidRequest is attempted once when this group's selected
	// credential returns a request-shape invalid_request error.
	FallbackGroupOnInvalidRequest string `yaml:"fallback-group-on-invalid-request,omitempty" json:"fallback-group-on-invalid-request,omitempty"`
}

// DefaultModelMappingRules returns the shipped default claude→GPT rules.
// opus/sonnet → latest model (gpt-5.5); haiku → small/fast (gpt-5.4-mini).
// These are advisory defaults: they are surfaced in config.example.yaml rather
// than seeded globally in code, so a deployment that also serves real Claude is
// never silently hijacked. Activate by adding a model-mapping block (or a group).
func DefaultModelMappingRules() []ModelMappingRule {
	return []ModelMappingRule{
		{Pattern: "claude-opus-*", Target: "gpt-5.5"},
		{Pattern: "claude-sonnet-*", Target: "gpt-5.5"},
		{Pattern: "claude-haiku-*", Target: "gpt-5.4-mini"},
	}
}

// SanitizeModelMapping normalizes the global model-mapping table.
func (cfg *Config) SanitizeModelMapping() {
	if cfg == nil {
		return
	}
	cfg.ModelMapping.Rules = sanitizeRules(cfg.ModelMapping.Rules)
}

// SanitizeGroups normalizes group definitions: trims names, dedupes api-keys,
// sorts each group's mapping rules, validates that no api-key is bound to two
// groups, and drops groups without a name. It mutates cfg.Groups in place.
func (cfg *Config) SanitizeGroups() {
	if cfg == nil || len(cfg.Groups) == 0 {
		return
	}
	clean := make([]GroupConfig, 0, len(cfg.Groups))
	keyOwner := make(map[string]string)
	for _, g := range cfg.Groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}
		keys := make([]string, 0, len(g.APIKeys))
		for _, k := range g.APIKeys {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if owner, dup := keyOwner[k]; dup {
				// An api-key may only belong to one group; keep the first binding.
				_ = owner
				continue
			}
			keyOwner[k] = name
			keys = append(keys, k)
		}
		providers := make([]string, 0, len(g.Providers))
		for _, p := range g.Providers {
			p = strings.ToLower(strings.TrimSpace(p))
			if p != "" {
				providers = append(providers, p)
			}
		}
		creds := make([]string, 0, len(g.Credentials))
		for _, c := range g.Credentials {
			c = strings.TrimSpace(c)
			if c != "" {
				creds = append(creds, c)
			}
		}
		models := make([]string, 0, len(g.Models))
		seenModels := make(map[string]struct{}, len(g.Models))
		for _, m := range g.Models {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			key := strings.ToLower(m)
			if _, dup := seenModels[key]; dup {
				continue
			}
			seenModels[key] = struct{}{}
			models = append(models, m)
		}
		g.Name = name
		g.APIKeys = keys
		g.Providers = providers
		g.Credentials = creds
		g.Models = models
		g.ModelMapping.Rules = sanitizeRules(g.ModelMapping.Rules)
		g.FallbackGroup = strings.TrimSpace(g.FallbackGroup)
		g.FallbackGroupOnInvalidRequest = strings.TrimSpace(g.FallbackGroupOnInvalidRequest)
		clean = append(clean, g)
	}
	cfg.Groups = clean
}

// GroupForAPIKey returns the group bound to the given inbound api key, or nil.
func GroupForAPIKey(groups []GroupConfig, apiKey string) *GroupConfig {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	for i := range groups {
		for _, k := range groups[i].APIKeys {
			if k == apiKey {
				return &groups[i]
			}
		}
	}
	return nil
}

// GroupHasModelScope reports whether grp restricts requested model names.
func GroupHasModelScope(grp *GroupConfig) bool {
	return grp != nil && (grp.DenyModels || len(grp.Models) > 0)
}

// GroupAllowsModel reports whether grp permits any of the supplied model names.
// Groups without a model scope keep legacy "all models" behavior. Matching is
// case-insensitive and accepts exact ids or filepath.Match-style globs.
func GroupAllowsModel(grp *GroupConfig, modelNames ...string) bool {
	if grp == nil || !GroupHasModelScope(grp) {
		return true
	}
	if grp.DenyModels {
		return false
	}
	for _, rawName := range modelNames {
		name := normalizeGroupModelCandidate(rawName)
		if name == "" {
			continue
		}
		for _, rawPattern := range grp.Models {
			pattern := normalizeGroupModelCandidate(rawPattern)
			if pattern == "" {
				continue
			}
			if pattern == name {
				return true
			}
			if ok, _ := filepath.Match(pattern, name); ok {
				return true
			}
		}
	}
	return false
}

func normalizeGroupModelCandidate(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "models/")
	return strings.ToLower(value)
}

// EffectiveModelContextOverrides merges explicit ModelContextOverrides with any
// per-rule ContextLength from the global and per-group model-mapping tables, keyed
// by upstream model id. Explicit overrides win over rule-derived values. Returns
// nil when nothing is configured.
func (cfg *Config) EffectiveModelContextOverrides() map[string]int {
	if cfg == nil {
		return nil
	}
	out := make(map[string]int)
	collect := func(rules []ModelMappingRule) {
		for _, r := range rules {
			target := strings.TrimSpace(r.Target)
			if r.ContextLength > 0 && target != "" {
				out[target] = r.ContextLength
			}
		}
	}
	collect(cfg.ModelMapping.Rules)
	for i := range cfg.Groups {
		collect(cfg.Groups[i].ModelMapping.Rules)
	}
	for k, v := range cfg.ModelContextOverrides {
		k = strings.TrimSpace(k)
		if k != "" && v > 0 {
			out[k] = v // explicit map wins over rule-derived values
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GroupByName returns the named group, or nil.
func GroupByName(groups []GroupConfig, name string) *GroupConfig {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i]
		}
	}
	return nil
}
