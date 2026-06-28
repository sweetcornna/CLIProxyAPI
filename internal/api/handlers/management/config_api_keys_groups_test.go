package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher/synthesizer"
)

func TestPutAPIKeysBindsNewKeysToDefaultGroup(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-existing"},
				Groups: []config.GroupConfig{
					{Name: "team-a", APIKeys: []string{"sk-existing"}},
				},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/api-keys", strings.NewReader(`["sk-existing","sk-new"]`))

	h.PutAPIKeys(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if g := config.GroupForAPIKey(h.cfg.Groups, "sk-existing"); g == nil || g.Name != "team-a" {
		t.Fatalf("sk-existing group = %+v, want team-a", g)
	}
	if g := config.GroupForAPIKey(h.cfg.Groups, "sk-new"); g == nil || g.Name != "default" {
		t.Fatalf("sk-new group = %+v, want default", g)
	}
}

func TestPatchAPIKeysRenamesGroupBinding(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-old"},
				Groups: []config.GroupConfig{
					{Name: "team-a", APIKeys: []string{"sk-old"}},
				},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-keys", strings.NewReader(`{"old":"sk-old","new":"sk-new"}`))

	h.PatchAPIKeys(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if g := config.GroupForAPIKey(h.cfg.Groups, "sk-new"); g == nil || g.Name != "team-a" {
		t.Fatalf("sk-new group = %+v, want team-a", g)
	}
	if g := config.GroupForAPIKey(h.cfg.Groups, "sk-old"); g != nil {
		t.Fatalf("sk-old should no longer be grouped, got %+v", g)
	}
}

func TestDeleteAPIKeysRemovesGroupBinding(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-delete", "sk-keep"},
				Groups: []config.GroupConfig{
					{Name: "team-a", APIKeys: []string{"sk-delete", "sk-keep"}},
				},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/api-keys?value=sk-delete", nil)

	h.DeleteAPIKeys(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if g := config.GroupForAPIKey(h.cfg.Groups, "sk-delete"); g != nil {
		t.Fatalf("sk-delete should no longer be grouped, got %+v", g)
	}
	if g := config.GroupForAPIKey(h.cfg.Groups, "sk-keep"); g == nil || g.Name != "team-a" {
		t.Fatalf("sk-keep group = %+v, want team-a", g)
	}
}

func TestPutGeminiKeysRejectsNewCredentialOutsideGroups(t *testing.T) {
	t.Parallel()

	idGen := synthesizer.NewStableIDGenerator()
	oldAuthID, _ := idGen.Next("gemini:apikey", "gemini-old", "https://generativelanguage.googleapis.com")
	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-team"},
				Groups: []config.GroupConfig{
					{Name: "team", APIKeys: []string{"sk-team"}, Credentials: []string{oldAuthID}},
				},
			},
			GeminiKey: []config.GeminiKey{
				{APIKey: "gemini-old", BaseURL: "https://generativelanguage.googleapis.com"},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/gemini-api-key", strings.NewReader(`[
		{"api-key":"gemini-old","base-url":"https://generativelanguage.googleapis.com"},
		{"api-key":"gemini-new","base-url":"https://generativelanguage.googleapis.com"}
	]`))

	h.PutGeminiKeys(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "gemini-new") {
		t.Fatalf("response should not leak raw api key, body=%s", rec.Body.String())
	}
	if len(h.cfg.GeminiKey) != 1 || h.cfg.GeminiKey[0].APIKey != "gemini-old" {
		t.Fatalf("gemini keys mutated after rejected update: %+v", h.cfg.GeminiKey)
	}
}

func TestPutGeminiKeysAllowsNewCredentialCoveredByProviderGroup(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-team"},
				Groups: []config.GroupConfig{
					{Name: "team", APIKeys: []string{"sk-team"}, Providers: []string{"gemini"}},
				},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/gemini-api-key", strings.NewReader(`[
		{"api-key":"gemini-new","base-url":"https://generativelanguage.googleapis.com"}
	]`))

	h.PutGeminiKeys(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(h.cfg.GeminiKey) != 1 || h.cfg.GeminiKey[0].APIKey != "gemini-new" {
		t.Fatalf("gemini keys = %+v, want new key persisted", h.cfg.GeminiKey)
	}
}

func TestPatchGeminiKeyRejectsCredentialRenameOutsideGroups(t *testing.T) {
	t.Parallel()

	idGen := synthesizer.NewStableIDGenerator()
	oldAuthID, _ := idGen.Next("gemini:apikey", "gemini-old", "https://generativelanguage.googleapis.com")
	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-team"},
				Groups: []config.GroupConfig{
					{Name: "team", APIKeys: []string{"sk-team"}, Credentials: []string{oldAuthID}},
				},
			},
			GeminiKey: []config.GeminiKey{
				{APIKey: "gemini-old", BaseURL: "https://generativelanguage.googleapis.com"},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/gemini-api-key", strings.NewReader(`{
		"index":0,
		"value":{"api-key":"gemini-new"}
	}`))

	h.PatchGeminiKey(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "gemini-new") {
		t.Fatalf("response should not leak raw api key, body=%s", rec.Body.String())
	}
	if len(h.cfg.GeminiKey) != 1 || h.cfg.GeminiKey[0].APIKey != "gemini-old" {
		t.Fatalf("gemini keys mutated after rejected patch: %+v", h.cfg.GeminiKey)
	}
}
