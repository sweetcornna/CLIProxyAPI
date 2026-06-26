package config

import "testing"

func TestParseConfigBytes_ForkSections(t *testing.T) {
	yaml := []byte(`
api-keys: ["sk-a", "sk-b"]
model-mapping:
  enabled: true
  rules:
    - { pattern: "claude-*", target: "gpt-fallback" }
    - { pattern: "claude-opus-*", target: "gpt-5.5", context-length: 400000 }
model-context-overrides:
  gpt-5.4: 1050000
groups:
  - name: "longctx"
    api-keys: ["sk-1m"]
    providers: ["Codex"]
    model-mapping:
      rules:
        - { pattern: "claude-*", target: "gpt-5.4" }
    fallback-group: "default"
claude-compat:
  enabled: true
  subagent-nudge: true
`)
	cfg, err := ParseConfigBytes(yaml)
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	// Global mapping sanitized + longest-wins ordering.
	if !cfg.ModelMapping.IsEnabled() || len(cfg.ModelMapping.Rules) != 2 {
		t.Fatalf("global model-mapping not parsed: %+v", cfg.ModelMapping)
	}
	if cfg.ModelMapping.Rules[0].Pattern != "claude-opus-*" {
		t.Errorf("rules not sorted longest-first: %+v", cfg.ModelMapping.Rules)
	}
	// Effective context overrides merge explicit map + per-rule context-length.
	ov := cfg.EffectiveModelContextOverrides()
	if ov["gpt-5.5"] != 400000 || ov["gpt-5.4"] != 1050000 {
		t.Errorf("effective context overrides wrong: %+v", ov)
	}
	// Group parsed, provider lowercased, model-mapping present.
	if g := GroupForAPIKey(cfg.Groups, "sk-1m"); g == nil || g.Providers[0] != "codex" || len(g.ModelMapping.Rules) != 1 {
		t.Errorf("group not parsed/sanitized: %+v", cfg.Groups)
	}
	// Compat enabled by default.
	if !cfg.ClaudeCompat.SubagentNudgeEnabled() {
		t.Errorf("claude-compat should be enabled")
	}
}

func ptrBool(b bool) *bool { return &b }

func TestResolveMappedModel_ExactBeatsWildcard(t *testing.T) {
	cfg := &Config{}
	cfg.ModelMapping.Rules = []ModelMappingRule{
		{Pattern: "claude-opus-*", Target: "gpt-5.5"},
		{Pattern: "claude-opus-4-5-20251101", Target: "gpt-5.4"},
	}
	cfg.SanitizeModelMapping()

	got, ok := ResolveMappedModel(cfg.ModelMapping.Rules, "claude-opus-4-5-20251101")
	if !ok || got != "gpt-5.4" {
		t.Fatalf("exact match should win: got %q ok=%v", got, ok)
	}
	got, ok = ResolveMappedModel(cfg.ModelMapping.Rules, "claude-opus-4-1-20250805")
	if !ok || got != "gpt-5.5" {
		t.Fatalf("wildcard fallback: got %q ok=%v", got, ok)
	}
}

func TestResolveMappedModel_LongestWildcardWins(t *testing.T) {
	cfg := &Config{}
	cfg.ModelMapping.Rules = []ModelMappingRule{
		{Pattern: "claude-*", Target: "gpt-fallback"},
		{Pattern: "claude-haiku-*", Target: "gpt-5.4-mini"},
		{Pattern: "claude-sonnet-*", Target: "gpt-5.5"},
	}
	cfg.SanitizeModelMapping()

	cases := map[string]string{
		"claude-haiku-4-5-20251001":  "gpt-5.4-mini",
		"claude-sonnet-4-5-20250929": "gpt-5.5",
		"claude-3-7-mystery":         "gpt-fallback",
	}
	for in, want := range cases {
		got, ok := ResolveMappedModel(cfg.ModelMapping.Rules, in)
		if !ok || got != want {
			t.Errorf("ResolveMappedModel(%q) = %q ok=%v, want %q", in, got, ok, want)
		}
	}
}

func TestResolveMappedModel_CaseInsensitiveAndNoMatch(t *testing.T) {
	rules := sanitizeRules([]ModelMappingRule{{Pattern: "claude-opus-*", Target: "gpt-5.5"}})
	if got, ok := ResolveMappedModel(rules, "CLAUDE-OPUS-4-1"); !ok || got != "gpt-5.5" {
		t.Fatalf("case-insensitive match failed: got %q ok=%v", got, ok)
	}
	if _, ok := ResolveMappedModel(rules, "gpt-5.5"); ok {
		t.Fatalf("non-claude name should not match")
	}
	if _, ok := ResolveMappedModel(nil, "claude-opus-4"); ok {
		t.Fatalf("empty rules should not match")
	}
}

func TestModelMappingConfig_IsEnabled(t *testing.T) {
	if (ModelMappingConfig{}).IsEnabled() {
		t.Errorf("empty config should be disabled")
	}
	if !(ModelMappingConfig{Rules: []ModelMappingRule{{Pattern: "claude-*", Target: "gpt-5.5"}}}).IsEnabled() {
		t.Errorf("config with rules should default enabled")
	}
	if (ModelMappingConfig{Enabled: ptrBool(false), Rules: []ModelMappingRule{{Pattern: "claude-*", Target: "x"}}}).IsEnabled() {
		t.Errorf("explicit disable should win over rules")
	}
}

func TestSanitizeGroups_DedupesKeysAndPreventsDoubleBinding(t *testing.T) {
	cfg := &Config{}
	cfg.Groups = []GroupConfig{
		{Name: "a", APIKeys: []string{" sk-1 ", "sk-1", "sk-2"}, Providers: []string{" Codex "}},
		{Name: "b", APIKeys: []string{"sk-2", "sk-3"}}, // sk-2 already owned by "a"
		{Name: "", APIKeys: []string{"sk-x"}},          // dropped: no name
	}
	cfg.SanitizeGroups()

	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	if g := GroupForAPIKey(cfg.Groups, "sk-1"); g == nil || g.Name != "a" {
		t.Errorf("sk-1 should map to group a")
	}
	if g := GroupForAPIKey(cfg.Groups, "sk-2"); g == nil || g.Name != "a" {
		t.Errorf("sk-2 should stay bound to group a (first binding wins)")
	}
	if g := GroupForAPIKey(cfg.Groups, "sk-3"); g == nil || g.Name != "b" {
		t.Errorf("sk-3 should map to group b")
	}
	if cfg.Groups[0].Providers[0] != "codex" {
		t.Errorf("providers should be trimmed+lowercased, got %q", cfg.Groups[0].Providers[0])
	}
}
