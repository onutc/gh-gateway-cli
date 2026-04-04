package gatewayconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActiveTokenFromRuntimeExchangeEnv(t *testing.T) {
	t.Helper()

	subjectTokenPath := writeTempFile(t, "subject-token", "subject-token-value\n")
	cacheDir := t.TempDir()

	var runtimeCalls int
	var gatewayCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime-exchange":
			runtimeCalls++
			writeJSON(t, w, map[string]any{
				"data": map[string]any{
					"access_token": "runtime-token",
					"expires_in":   3600,
				},
			})
		case "/gateway-exchange":
			gatewayCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
				t.Fatalf("Authorization header = %q", got)
			}
			writeJSON(t, w, map[string]any{
				"data": map[string]any{
					"access_token":     "gateway-token",
					"gateway_base_url": "http://gateway.example.test",
					"expires_in":       3600,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv(envRuntimeExchangeURL, server.URL+"/runtime-exchange")
	t.Setenv(envAccessExchangeURL, server.URL+"/gateway-exchange")
	t.Setenv(envInstanceID, "instance-123")
	t.Setenv(envSubjectTokenFile, subjectTokenPath)
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	token, source := ActiveToken()
	if token != "gateway-token" {
		t.Fatalf("ActiveToken() token = %q", token)
	}
	if source != envRuntimeExchangeURL {
		t.Fatalf("ActiveToken() source = %q", source)
	}
	if runtimeCalls != 1 || gatewayCalls != 1 {
		t.Fatalf("unexpected exchange counts: runtime=%d gateway=%d", runtimeCalls, gatewayCalls)
	}

	if got := BrowserBaseURL(); got != "http://gateway.example.test/" {
		t.Fatalf("BrowserBaseURL() = %q", got)
	}
	if got := RESTBaseURL(); got != "http://gateway.example.test/api/v3/" {
		t.Fatalf("RESTBaseURL() = %q", got)
	}
	if got := GraphQLURL(); got != "http://gateway.example.test/api/graphql" {
		t.Fatalf("GraphQLURL() = %q", got)
	}

	token, source = ActiveToken()
	if token != "gateway-token" || source != envRuntimeExchangeURL {
		t.Fatalf("second ActiveToken() = %q, %q", token, source)
	}
	if runtimeCalls != 1 || gatewayCalls != 1 {
		t.Fatalf("expected cached token to avoid exchanges: runtime=%d gateway=%d", runtimeCalls, gatewayCalls)
	}
}

func TestActiveTokenUsesCompatibleDiskCache(t *testing.T) {
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, defaultGatewayCacheFile)
	cachePayload, err := json.Marshal(cachedGatewayAccess{
		AccessToken:    "cached-token",
		GatewayBaseURL: "http://gateway.example.test",
		InstanceID:     "instance-123",
		ExpiresAtMs:    time.Now().Add(time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, append(cachePayload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envRuntimeExchangeURL, "http://example.com/runtime-exchange")
	t.Setenv(envAccessExchangeURL, "http://example.com/gateway-exchange")
	t.Setenv(envInstanceID, "instance-123")
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	token, source := ActiveToken()
	if token != "cached-token" {
		t.Fatalf("ActiveToken() token = %q", token)
	}
	if source != envRuntimeExchangeURL {
		t.Fatalf("ActiveToken() source = %q", source)
	}
	if got := BrowserBaseURL(); got != "http://gateway.example.test/" {
		t.Fatalf("BrowserBaseURL() = %q", got)
	}
}

func writeTempFile(t *testing.T, name string, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}
