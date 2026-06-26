package auth

import (
	"path/filepath"
	"strings"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// groupFromMetadata resolves the account group named in opts.Metadata, or nil.
func (m *Manager) groupFromMetadata(meta map[string]any) *internalconfig.GroupConfig {
	name := metadataGroupName(meta)
	if name == "" {
		return nil
	}
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	if cfg == nil {
		return nil
	}
	return internalconfig.GroupByName(cfg.Groups, name)
}

func metadataGroupName(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	if raw, ok := meta[cliproxyexecutor.GroupNameMetadataKey]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// authMatchesGroup reports whether auth belongs to grp's credential selection.
// A group with neither Providers nor Credentials matches every auth (it only
// scopes per-group model mapping, not credential narrowing).
func authMatchesGroup(auth *Auth, grp *internalconfig.GroupConfig) bool {
	if auth == nil || grp == nil {
		return false
	}
	if len(grp.Providers) == 0 && len(grp.Credentials) == 0 {
		return true
	}
	if len(grp.Providers) > 0 {
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		matched := false
		for _, p := range grp.Providers {
			if p == provider {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(grp.Credentials) > 0 && !authLabelMatchesAny(auth, grp.Credentials) {
		return false
	}
	return true
}

// authLabelMatchesAny matches an auth's label/id (and their basenames) against a
// set of glob patterns (filepath.Match syntax, e.g. "codex-pro-*").
func authLabelMatchesAny(auth *Auth, patterns []string) bool {
	candidates := []string{auth.Label, auth.ID}
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		for _, c := range candidates {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if ok, _ := filepath.Match(pat, c); ok {
				return true
			}
			if ok, _ := filepath.Match(pat, filepath.Base(c)); ok {
				return true
			}
		}
	}
	return false
}

// groupCredentialNarrowing reports whether grp restricts the credential set at all.
func groupCredentialNarrowing(grp *internalconfig.GroupConfig) bool {
	return grp != nil && (len(grp.Providers) > 0 || len(grp.Credentials) > 0)
}

// seedOutOfGroupTried marks every auth that is NOT part of grp's credential
// selection as already-tried, so the scheduler skips them. This is the minimal,
// both-path-safe narrowing mechanism (the tried set is honored by the fast-path
// scheduler and the legacy selector alike). A group without credential filters is
// a no-op (all auths remain selectable).
func (m *Manager) seedOutOfGroupTried(grp *internalconfig.GroupConfig, tried map[string]struct{}) {
	if !groupCredentialNarrowing(grp) || tried == nil {
		return
	}
	for _, a := range m.snapshotAuths() {
		if a == nil || a.ID == "" {
			continue
		}
		if !authMatchesGroup(a, grp) {
			tried[a.ID] = struct{}{}
		}
	}
}

// resolveFallbackGroups returns the ordered fallback groups for grp, following
// fallback-group links and guarding against cycles. The primary group itself is
// excluded. Returns nil when no (usable) fallback is configured.
func (m *Manager) resolveFallbackGroups(grp *internalconfig.GroupConfig) []*internalconfig.GroupConfig {
	if grp == nil || strings.TrimSpace(grp.FallbackGroup) == "" {
		return nil
	}
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	if cfg == nil {
		return nil
	}
	out := make([]*internalconfig.GroupConfig, 0, 4)
	seen := map[string]struct{}{grp.Name: {}}
	name := strings.TrimSpace(grp.FallbackGroup)
	for name != "" {
		if _, dup := seen[name]; dup {
			break
		}
		seen[name] = struct{}{}
		next := internalconfig.GroupByName(cfg.Groups, name)
		if next == nil {
			break
		}
		out = append(out, next)
		name = strings.TrimSpace(next.FallbackGroup)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// admitGroupAuths removes grp's matching auths from the tried set so the scheduler
// may select them. Used when falling back to a secondary group after the primary
// group's credentials are exhausted.
func (m *Manager) admitGroupAuths(grp *internalconfig.GroupConfig, tried map[string]struct{}) {
	if grp == nil || tried == nil {
		return
	}
	for _, a := range m.snapshotAuths() {
		if a == nil || a.ID == "" {
			continue
		}
		if authMatchesGroup(a, grp) {
			delete(tried, a.ID)
		}
	}
}
