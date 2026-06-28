package management

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func authFileAllowedGroupsFromRequest(c *gin.Context) ([]string, error) {
	if c == nil || c.Request == nil {
		return nil, nil
	}
	keys := []string{"group", "groups", "allowed_group", "allowed_groups"}
	rawValues := make([]string, 0)
	for _, key := range keys {
		rawValues = append(rawValues, c.QueryArray(key)...)
	}
	if form := c.Request.MultipartForm; form != nil {
		for _, key := range keys {
			rawValues = append(rawValues, form.Value[key]...)
		}
	}
	return parseAuthFileAllowedGroupValues(rawValues)
}

func parseAuthFileAllowedGroupValues(rawValues []string) ([]string, error) {
	seen := make(map[string]struct{}, len(rawValues))
	out := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var items []string
		if strings.HasPrefix(raw, "[") {
			if err := json.Unmarshal([]byte(raw), &items); err != nil {
				return nil, authFileGroupValidationError{message: "invalid groups"}
			}
		} else {
			items = strings.Split(raw, ",")
		}
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out, nil
}

func (h *Handler) nextConfigWithAuthFileGroupBindings(path string, auth *coreauth.Auth, allowedGroups []string) (*config.Config, bool, error) {
	if h == nil || auth == nil {
		return nil, false, nil
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	authID := strings.TrimSpace(auth.ID)
	if provider == "" || authID == "" {
		return nil, false, authFileGroupValidationError{message: "auth file did not produce a usable credential"}
	}

	cfg := h.cfg
	if len(allowedGroups) == 0 {
		if cfg != nil && h.authFileCredentialIsNew(path, authID) && groupsHaveExplicitCredentialBoundary(cfg.Groups) &&
			!configAPIKeyAuthCoveredByExplicitGroup(cfg.Groups, configAPIKeyAuthRef{Provider: provider, ID: authID}) {
			return nil, false, authFileGroupValidationError{message: fmt.Sprintf("new auth file credential is not bound to any group: provider=%s auth_id=%s", provider, authID)}
		}
		return nil, false, nil
	}
	if cfg == nil {
		return nil, false, authFileGroupValidationError{message: "config unavailable"}
	}

	nextCfg := cfg.CloneForRuntime()
	if nextCfg == nil {
		return nil, false, authFileGroupValidationError{message: "config unavailable"}
	}
	for _, groupName := range allowedGroups {
		idx := indexGroupByName(nextCfg.Groups, groupName)
		if idx < 0 {
			return nil, false, authFileGroupValidationError{message: fmt.Sprintf("group not found: %s", groupName)}
		}
		group := &nextCfg.Groups[idx]
		group.DenyCredentials = false
		group.Providers = appendStringIfMissingFold(group.Providers, provider)
		group.Credentials = appendStringIfMissing(group.Credentials, authID)
	}
	nextCfg.SanitizeGroups()
	return nextCfg, true, nil
}

func (h *Handler) authFileCredentialIsNew(path string, authID string) bool {
	if h != nil && h.authManager != nil {
		if _, ok := h.authManager.GetByID(authID); ok {
			return false
		}
	}
	if strings.TrimSpace(path) != "" {
		if _, err := os.Stat(path); err == nil {
			return false
		}
	}
	return true
}

func indexGroupByName(groups []config.GroupConfig, name string) int {
	name = strings.TrimSpace(name)
	for i := range groups {
		if strings.TrimSpace(groups[i].Name) == name {
			return i
		}
	}
	return -1
}

func appendStringIfMissing(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if strings.TrimSpace(item) == value {
			return items
		}
	}
	return append(items, value)
}

func appendStringIfMissingFold(items []string, value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return items
	}
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item)) == value {
			return items
		}
	}
	return append(items, value)
}

func (h *Handler) saveAuthFileGroupConfig(ctx context.Context, nextCfg *config.Config) error {
	if h == nil || nextCfg == nil {
		return nil
	}
	h.mu.Lock()
	oldCfg := h.cfg
	h.cfg = nextCfg
	if errSave := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); errSave != nil {
		h.cfg = oldCfg
		h.mu.Unlock()
		return fmt.Errorf("failed to save config: %w", errSave)
	}
	snapshot := h.reloadSnapshotConfigLocked()
	h.mu.Unlock()
	h.reloadConfigAfterManagementSave(ctx, snapshot)
	return nil
}

func (h *Handler) rollbackAuthFileWrite(ctx context.Context, path string, hadOldFile bool, oldData []byte, oldAuth *coreauth.Auth, newAuthID string) {
	if hadOldFile {
		_ = os.WriteFile(path, oldData, 0o600)
	} else {
		_ = os.Remove(path)
	}
	if h == nil || h.authManager == nil {
		return
	}
	if oldAuth != nil {
		if _, err := h.authManager.Update(ctx, oldAuth); err != nil {
			_, _ = h.authManager.Register(ctx, oldAuth)
		}
		return
	}
	h.removeAuth(ctx, newAuthID)
}
