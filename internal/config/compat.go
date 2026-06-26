package config

// CompatConfig configures relay-layer "Claude Code compatibility" prompt injection.
// It is independent of cloaking (which disguises identity): compat injection nudges
// a GPT/Codex backend to behave well as a Claude Code agent. Currently it carries a
// single, scoped sub-agent-dispatch nudge that fires only when a Claude Code request
// is mapped to a non-Claude (GPT) upstream and exposes a dispatch (Task/Agent) tool.
type CompatConfig struct {
	// Enabled is the master switch for compat injection. nil defaults to true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// SubagentNudge toggles the sub-agent dispatch nudge. nil defaults to true.
	SubagentNudge *bool `yaml:"subagent-nudge,omitempty" json:"subagent-nudge,omitempty"`

	// SubagentNudgeText overrides the built-in nudge text. Empty uses the default.
	SubagentNudgeText string `yaml:"subagent-nudge-text,omitempty" json:"subagent-nudge-text,omitempty"`
}

// SubagentNudgeEnabled reports whether the sub-agent dispatch nudge should be
// applied. Both switches default to true, so a zero-value CompatConfig is enabled.
func (c CompatConfig) SubagentNudgeEnabled() bool {
	enabled := c.Enabled == nil || *c.Enabled
	nudge := c.SubagentNudge == nil || *c.SubagentNudge
	return enabled && nudge
}
