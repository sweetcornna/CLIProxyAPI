package helps

import (
	"context"
	"strings"
	"sync"
	"time"

	homekv "github.com/router-for-me/CLIProxyAPI/v7/internal/home"
)

type CodexCache struct {
	ID     string
	Expire time.Time
}

const codexPreviousResponseContinuationDisabled = "__cpa_previous_response_continuation_disabled__"

// codexCacheMap stores prompt cache IDs keyed by a scoped user/session cache key.
// Protected by codexCacheMu. Entries expire after 1 hour.
var (
	codexCacheMap = make(map[string]CodexCache)
	codexCacheMu  sync.RWMutex
)

// codexCacheCleanupInterval controls how often expired entries are purged.
const codexCacheCleanupInterval = 15 * time.Minute

// codexCacheCleanupOnce ensures the background cleanup goroutine starts only once.
var codexCacheCleanupOnce sync.Once

// startCodexCacheCleanup launches a background goroutine that periodically
// removes expired entries from codexCacheMap to prevent memory leaks.
func startCodexCacheCleanup() {
	go func() {
		ticker := time.NewTicker(codexCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredCodexCache()
		}
	}()
}

// purgeExpiredCodexCache removes entries that have expired.
func purgeExpiredCodexCache() {
	now := time.Now()
	codexCacheMu.Lock()
	defer codexCacheMu.Unlock()
	for key, cache := range codexCacheMap {
		if cache.Expire.Before(now) {
			delete(codexCacheMap, key)
		}
	}
}

// GetCodexCache retrieves a cached entry, returning ok=false if not found or expired.
func GetCodexCache(key string) (CodexCache, bool) {
	cache, ok, err := GetCodexCacheRequired(context.Background(), key)
	if err == nil {
		return cache, ok
	}
	return CodexCache{}, false
}

// GetCodexCacheRequired retrieves a cached entry for request-time paths.
func GetCodexCacheRequired(ctx context.Context, key string) (CodexCache, bool, error) {
	var homeCache CodexCache
	homeMode, found, errGet := homekv.KVGetJSONRequired(ctx, key, &homeCache)
	if homeMode {
		if errGet != nil || !found {
			return CodexCache{}, false, errGet
		}
		if homeCache.Expire.Before(time.Now()) {
			_, _, _ = homekv.KVDelRequired(ctx, key)
			return CodexCache{}, false, nil
		}
		return homeCache, true, nil
	}

	codexCacheCleanupOnce.Do(startCodexCacheCleanup)
	codexCacheMu.RLock()
	cache, ok := codexCacheMap[key]
	codexCacheMu.RUnlock()
	if !ok || cache.Expire.Before(time.Now()) {
		return CodexCache{}, false, nil
	}
	return cache, true, nil
}

// SetCodexCache stores a cache entry.
func SetCodexCache(key string, cache CodexCache) {
	SetCodexCacheBestEffort(context.Background(), key, cache)
}

// SetCodexCacheRequired stores a cache entry for request-time paths.
func SetCodexCacheRequired(ctx context.Context, key string, cache CodexCache) error {
	ttl := time.Until(cache.Expire)
	if ttl <= 0 {
		return nil
	}
	if _, homeMode, _ := homekv.CurrentKVClient(); homeMode {
		_, errSet := homekv.KVSetJSONRequired(ctx, key, cache, ttl)
		return errSet
	}
	codexCacheCleanupOnce.Do(startCodexCacheCleanup)
	codexCacheMu.Lock()
	codexCacheMap[key] = cache
	codexCacheMu.Unlock()
	return nil
}

// SetCodexCacheBestEffort stores a cache entry without failing completed responses.
func SetCodexCacheBestEffort(ctx context.Context, key string, cache CodexCache) bool {
	ttl := time.Until(cache.Expire)
	if ttl <= 0 {
		return false
	}
	if _, homeMode, _ := homekv.CurrentKVClient(); homeMode {
		return homekv.KVSetJSONBestEffort(ctx, key, cache, ttl)
	}
	codexCacheCleanupOnce.Do(startCodexCacheCleanup)
	codexCacheMu.Lock()
	codexCacheMap[key] = cache
	codexCacheMu.Unlock()
	return true
}

// CodexPromptCacheKey builds the Home KV key for a model/user prompt cache.
func CodexPromptCacheKey(modelName string, userScope string) string {
	return "cpa:codex:prompt-cache:" + homekv.HashKeyPart(modelName) + ":" + homekv.HashKeyPart(userScope)
}

// CodexTurnStateKey stores ChatGPT/Codex turn-state headers by the upstream
// prompt cache key so OAuth-style Codex sessions can resume warm context.
func CodexTurnStateKey(promptCacheKey string) string {
	return "cpa:codex:turn-state:" + homekv.HashKeyPart(promptCacheKey)
}

func GetCodexTurnStateRequired(ctx context.Context, promptCacheKey string) (string, bool, error) {
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if promptCacheKey == "" {
		return "", false, nil
	}
	cache, ok, err := GetCodexCacheRequired(ctx, CodexTurnStateKey(promptCacheKey))
	if err != nil || !ok {
		return "", false, err
	}
	turnState := strings.TrimSpace(cache.ID)
	if turnState == "" {
		return "", false, nil
	}
	return turnState, true, nil
}

func SetCodexTurnStateBestEffort(ctx context.Context, promptCacheKey string, turnState string) bool {
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	turnState = strings.TrimSpace(turnState)
	if promptCacheKey == "" || turnState == "" {
		return false
	}
	return SetCodexCacheBestEffort(ctx, CodexTurnStateKey(promptCacheKey), CodexCache{
		ID:     turnState,
		Expire: time.Now().Add(1 * time.Hour),
	})
}

// CodexPreviousResponseKey stores API-key Codex response continuation state by
// auth scope and upstream prompt cache key, mirroring the upstream cache scope.
func CodexPreviousResponseKey(authScope string, promptCacheKey string) string {
	return "cpa:codex:previous-response:" + homekv.HashKeyPart(authScope) + ":" + homekv.HashKeyPart(promptCacheKey)
}

func GetCodexPreviousResponseIDRequired(ctx context.Context, authScope string, promptCacheKey string) (string, bool, error) {
	authScope = strings.TrimSpace(authScope)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if authScope == "" || promptCacheKey == "" {
		return "", false, nil
	}
	cache, ok, err := GetCodexCacheRequired(ctx, CodexPreviousResponseKey(authScope, promptCacheKey))
	if err != nil || !ok {
		return "", false, err
	}
	responseID := strings.TrimSpace(cache.ID)
	if responseID == codexPreviousResponseContinuationDisabled {
		return "", false, nil
	}
	if responseID == "" {
		return "", false, nil
	}
	return responseID, true, nil
}

func SetCodexPreviousResponseIDBestEffort(ctx context.Context, authScope string, promptCacheKey string, responseID string) bool {
	authScope = strings.TrimSpace(authScope)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	responseID = strings.TrimSpace(responseID)
	if authScope == "" || promptCacheKey == "" || responseID == "" {
		return false
	}
	return SetCodexCacheBestEffort(ctx, CodexPreviousResponseKey(authScope, promptCacheKey), CodexCache{
		ID:     responseID,
		Expire: time.Now().Add(1 * time.Hour),
	})
}

func CodexPreviousResponseContinuationDisabledRequired(ctx context.Context, authScope string, promptCacheKey string) (bool, error) {
	authScope = strings.TrimSpace(authScope)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if authScope == "" || promptCacheKey == "" {
		return false, nil
	}
	cache, ok, err := GetCodexCacheRequired(ctx, CodexPreviousResponseKey(authScope, promptCacheKey))
	if err != nil || !ok {
		return false, err
	}
	return strings.TrimSpace(cache.ID) == codexPreviousResponseContinuationDisabled, nil
}

func DisableCodexPreviousResponseContinuationBestEffort(ctx context.Context, authScope string, promptCacheKey string) bool {
	authScope = strings.TrimSpace(authScope)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if authScope == "" || promptCacheKey == "" {
		return false
	}
	return SetCodexCacheBestEffort(ctx, CodexPreviousResponseKey(authScope, promptCacheKey), CodexCache{
		ID:     codexPreviousResponseContinuationDisabled,
		Expire: time.Now().Add(1 * time.Hour),
	})
}

func DeleteCodexPreviousResponseIDBestEffort(ctx context.Context, authScope string, promptCacheKey string) bool {
	authScope = strings.TrimSpace(authScope)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if authScope == "" || promptCacheKey == "" {
		return false
	}
	key := CodexPreviousResponseKey(authScope, promptCacheKey)
	if _, homeMode, _ := homekv.CurrentKVClient(); homeMode {
		return homekv.KVDelBestEffort(ctx, key)
	}
	codexCacheMu.Lock()
	delete(codexCacheMap, key)
	codexCacheMu.Unlock()
	return true
}

// ClaudeCodePromptCacheKey builds the Home KV key for a Claude Code session.
func ClaudeCodePromptCacheKey(sessionID string) string {
	return "cpa:codex:prompt-cache:claude-code:" + homekv.HashKeyPart(sessionID)
}

// ClaudeCodeDigestPromptCacheKey stores digest-chain prompt cache bindings for
// Claude Code requests that lost explicit session metadata.
func ClaudeCodeDigestPromptCacheKey(digestChain string) string {
	return "cpa:codex:prompt-cache:claude-code-digest:" + homekv.HashKeyPart(digestChain)
}
