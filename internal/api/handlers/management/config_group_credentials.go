package management

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher/synthesizer"
)

type configAPIKeyAuthRef struct {
	Provider string
	ID       string
}

func validateNewConfigAPIKeyGroupCoverage(oldCfg, nextCfg *config.Config) error {
	if oldCfg == nil || nextCfg == nil || !groupsHaveExplicitCredentialBoundary(nextCfg.Groups) {
		return nil
	}
	oldRefs := configAPIKeyAuthRefs(oldCfg)
	oldIDs := make(map[string]struct{}, len(oldRefs))
	for _, ref := range oldRefs {
		if ref.ID != "" {
			oldIDs[ref.ID] = struct{}{}
		}
	}
	for _, ref := range configAPIKeyAuthRefs(nextCfg) {
		if ref.ID == "" {
			continue
		}
		if _, existed := oldIDs[ref.ID]; existed {
			continue
		}
		if configAPIKeyAuthCoveredByExplicitGroup(nextCfg.Groups, ref) {
			continue
		}
		return fmt.Errorf("new config api key credential is not bound to any group: provider=%s auth_id=%s", ref.Provider, ref.ID)
	}
	return nil
}

func groupsHaveExplicitCredentialBoundary(groups []config.GroupConfig) bool {
	for _, group := range groups {
		if group.DenyCredentials || len(group.Providers) > 0 || len(group.Credentials) > 0 {
			return true
		}
	}
	return false
}

func configAPIKeyAuthCoveredByExplicitGroup(groups []config.GroupConfig, ref configAPIKeyAuthRef) bool {
	provider := strings.ToLower(strings.TrimSpace(ref.Provider))
	id := strings.TrimSpace(ref.ID)
	if provider == "" || id == "" {
		return false
	}
	for _, group := range groups {
		if group.DenyCredentials || (len(group.Providers) == 0 && len(group.Credentials) == 0) {
			continue
		}
		if len(group.Providers) > 0 && !groupProvidersContain(group.Providers, provider) {
			continue
		}
		if len(group.Credentials) > 0 && !credentialPatternsMatch(group.Credentials, id) {
			continue
		}
		return true
	}
	return false
}

func groupProvidersContain(providers []string, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, item := range providers {
		if strings.ToLower(strings.TrimSpace(item)) == provider {
			return true
		}
	}
	return false
}

func credentialPatternsMatch(patterns []string, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, _ := filepath.Match(pattern, id); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, filepath.Base(id)); ok {
			return true
		}
	}
	return false
}

func configAPIKeyAuthRefs(cfg *config.Config) []configAPIKeyAuthRef {
	if cfg == nil {
		return nil
	}
	idGen := synthesizer.NewStableIDGenerator()
	refs := make([]configAPIKeyAuthRef, 0, len(cfg.GeminiKey)+len(cfg.ClaudeKey)+len(cfg.CodexKey)+len(cfg.VertexCompatAPIKey)+len(cfg.OpenAICompatibility))
	for i := range cfg.GeminiKey {
		entry := cfg.GeminiKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		id, _ := idGen.Next("gemini:apikey", key, strings.TrimSpace(entry.BaseURL))
		refs = append(refs, configAPIKeyAuthRef{Provider: "gemini", ID: id})
	}
	for i := range cfg.ClaudeKey {
		entry := cfg.ClaudeKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		id, _ := idGen.Next("claude:apikey", key, strings.TrimSpace(entry.BaseURL))
		refs = append(refs, configAPIKeyAuthRef{Provider: "claude", ID: id})
	}
	for i := range cfg.CodexKey {
		entry := cfg.CodexKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		id, _ := idGen.Next("codex:apikey", key, strings.TrimSpace(entry.BaseURL))
		refs = append(refs, configAPIKeyAuthRef{Provider: "codex", ID: id})
	}
	for i := range cfg.OpenAICompatibility {
		entry := cfg.OpenAICompatibility[i]
		if entry.Disabled {
			continue
		}
		providerName := strings.ToLower(strings.TrimSpace(entry.Name))
		if providerName == "" {
			providerName = "openai-compatibility"
		}
		provider := util.OpenAICompatibleProviderKey(providerName)
		idKind := fmt.Sprintf("openai-compatibility:%s", providerName)
		base := strings.TrimSpace(entry.BaseURL)
		if len(entry.APIKeyEntries) == 0 {
			id, _ := idGen.Next(idKind, base)
			refs = append(refs, configAPIKeyAuthRef{Provider: provider, ID: id})
			continue
		}
		for j := range entry.APIKeyEntries {
			apiKeyEntry := entry.APIKeyEntries[j]
			id, _ := idGen.Next(idKind, strings.TrimSpace(apiKeyEntry.APIKey), base, strings.TrimSpace(apiKeyEntry.ProxyURL))
			refs = append(refs, configAPIKeyAuthRef{Provider: provider, ID: id})
		}
	}
	for i := range cfg.VertexCompatAPIKey {
		entry := cfg.VertexCompatAPIKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		id, _ := idGen.Next("vertex:apikey", key, strings.TrimSpace(entry.BaseURL), strings.TrimSpace(entry.ProxyURL))
		refs = append(refs, configAPIKeyAuthRef{Provider: "vertex", ID: id})
	}
	return refs
}
