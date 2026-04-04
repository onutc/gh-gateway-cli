package gatewayconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	envRuntimeExchangeURL      = "GH_GATEWAY_RUNTIME_EXCHANGE_URL"
	envAccessExchangeURL       = "GH_GATEWAY_ACCESS_EXCHANGE_URL"
	envInstanceID              = "GH_GATEWAY_INSTANCE_ID"
	envSubjectTokenFile        = "GH_GATEWAY_SUBJECT_TOKEN_FILE"
	envTokenCachePath          = "GH_GATEWAY_TOKEN_CACHE_PATH"
	envTokenRefreshSkewSeconds = "GH_GATEWAY_TOKEN_REFRESH_SKEW_SECONDS"

	defaultSubjectTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultTokenRefreshSkew = 60 * time.Second
	defaultGatewayCacheFile = "gh-gateway-cli-access.json"
	defaultExchangeTimeout  = 10 * time.Second
)

type runtimeExchangeConfig struct {
	runtimeExchangeURL string
	accessExchangeURL  string
	instanceID         string
	subjectTokenFile   string
	cachePath          string
	refreshSkew        time.Duration
}

type cachedGatewayAccess struct {
	AccessToken    string `json:"accessToken"`
	GatewayBaseURL string `json:"gatewayBaseUrl,omitempty"`
	InstanceID     string `json:"instanceId,omitempty"`
	ExpiresAtMs    int64  `json:"expiresAtMs"`
}

type runtimeExchangeDeps struct {
	getenv    func(string) string
	readFile  func(string) ([]byte, error)
	writeFile func(string, []byte, os.FileMode) error
	mkdirAll  func(string, os.FileMode) error
	rename    func(string, string) error
	now       func() time.Time
	client    *http.Client
}

func defaultRuntimeExchangeDeps() runtimeExchangeDeps {
	return runtimeExchangeDeps{
		getenv:    os.Getenv,
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
		rename:    os.Rename,
		now:       time.Now,
		client:    &http.Client{Timeout: defaultExchangeTimeout},
	}
}

func runtimeExchangeConfigFromEnv(getenv func(string) string) (runtimeExchangeConfig, bool) {
	runtimeExchangeURL := strings.TrimSpace(getenv(envRuntimeExchangeURL))
	accessExchangeURL := strings.TrimSpace(getenv(envAccessExchangeURL))
	if !validateURL(runtimeExchangeURL) || !validateURL(accessExchangeURL) {
		return runtimeExchangeConfig{}, false
	}

	cachePath := strings.TrimSpace(getenv(envTokenCachePath))
	if cachePath == "" {
		cachePath = defaultGatewayCachePath(getenv)
	}

	return runtimeExchangeConfig{
		runtimeExchangeURL: runtimeExchangeURL,
		accessExchangeURL:  accessExchangeURL,
		instanceID:         strings.TrimSpace(getenv(envInstanceID)),
		subjectTokenFile: firstNonEmpty(
			strings.TrimSpace(getenv(envSubjectTokenFile)),
			defaultSubjectTokenFile,
		),
		cachePath:   cachePath,
		refreshSkew: tokenRefreshSkew(getenv),
	}, true
}

func defaultGatewayCachePath(getenv func(string) string) string {
	cacheHome := strings.TrimSpace(getenv("XDG_CACHE_HOME"))
	if cacheHome == "" {
		home := strings.TrimSpace(getenv("HOME"))
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home == "" {
			home = "/tmp"
		}
		cacheHome = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheHome, defaultGatewayCacheFile)
}

func tokenRefreshSkew(getenv func(string) string) time.Duration {
	raw := strings.TrimSpace(getenv(envTokenRefreshSkewSeconds))
	if raw == "" {
		return defaultTokenRefreshSkew
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultTokenRefreshSkew
	}
	return time.Duration(seconds) * time.Second
}

func builtInGatewayBaseURL() string {
	cfg, ok := runtimeExchangeConfigFromEnv(os.Getenv)
	if !ok {
		return ""
	}
	access, err := resolveGatewayAccess(cfg, defaultRuntimeExchangeDeps())
	if err != nil || access.GatewayBaseURL == "" {
		return ""
	}
	return ensureTrailingSlash(access.GatewayBaseURL)
}

func builtInActiveToken() (string, string) {
	cfg, ok := runtimeExchangeConfigFromEnv(os.Getenv)
	if !ok {
		return "", ""
	}
	access, err := resolveGatewayAccess(cfg, defaultRuntimeExchangeDeps())
	if err != nil || access.AccessToken == "" {
		return "", ""
	}
	return access.AccessToken, envRuntimeExchangeURL
}

func resolveGatewayAccess(cfg runtimeExchangeConfig, deps runtimeExchangeDeps) (cachedGatewayAccess, error) {
	now := deps.now()
	if cached, ok := readCachedGatewayAccess(cfg, deps, now); ok {
		return cached, nil
	}
	access, err := exchangeGatewayAccess(cfg, deps, now)
	if err != nil {
		return cachedGatewayAccess{}, err
	}
	_ = writeCachedGatewayAccess(cfg, deps, access)
	return access, nil
}

func readCachedGatewayAccess(cfg runtimeExchangeConfig, deps runtimeExchangeDeps, now time.Time) (cachedGatewayAccess, bool) {
	raw, err := deps.readFile(cfg.cachePath)
	if err != nil {
		return cachedGatewayAccess{}, false
	}

	var cached cachedGatewayAccess
	if err := json.Unmarshal(raw, &cached); err != nil {
		return cachedGatewayAccess{}, false
	}
	if !cachedGatewayAccessValid(cached, cfg, now) {
		return cachedGatewayAccess{}, false
	}
	return cached, true
}

func cachedGatewayAccessValid(cached cachedGatewayAccess, cfg runtimeExchangeConfig, now time.Time) bool {
	if strings.TrimSpace(cached.AccessToken) == "" {
		return false
	}
	if cfg.instanceID != "" && cached.InstanceID != "" && cached.InstanceID != cfg.instanceID {
		return false
	}
	expiresAt := time.UnixMilli(cached.ExpiresAtMs)
	return expiresAt.After(now.Add(cfg.refreshSkew))
}

func writeCachedGatewayAccess(cfg runtimeExchangeConfig, deps runtimeExchangeDeps, access cachedGatewayAccess) error {
	if cfg.cachePath == "" {
		return nil
	}
	directory := filepath.Dir(cfg.cachePath)
	if err := deps.mkdirAll(directory, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(access)
	if err != nil {
		return err
	}
	tempPath := fmt.Sprintf("%s.tmp-%d", cfg.cachePath, os.Getpid())
	if err := deps.writeFile(tempPath, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return deps.rename(tempPath, cfg.cachePath)
}

func exchangeGatewayAccess(cfg runtimeExchangeConfig, deps runtimeExchangeDeps, now time.Time) (cachedGatewayAccess, error) {
	subjectTokenBytes, err := deps.readFile(cfg.subjectTokenFile)
	if err != nil {
		return cachedGatewayAccess{}, fmt.Errorf("read subject token: %w", err)
	}
	subjectToken := strings.TrimSpace(string(subjectTokenBytes))
	if subjectToken == "" {
		return cachedGatewayAccess{}, errors.New("subject token is empty")
	}

	runtimeRequest := map[string]string{
		"subject_token": subjectToken,
	}
	if cfg.instanceID != "" {
		runtimeRequest["instance_id"] = cfg.instanceID
	}
	runtimePayload, err := postJSON(deps.client, cfg.runtimeExchangeURL, runtimeRequest, "")
	if err != nil {
		return cachedGatewayAccess{}, err
	}
	runtimeData := extractDataPayload(runtimePayload)
	runtimeToken := extractString(runtimeData, "access_token")
	if runtimeToken == "" {
		return cachedGatewayAccess{}, errors.New("runtime exchange did not return access_token")
	}

	accessPayload, err := postJSON(deps.client, cfg.accessExchangeURL, map[string]string{}, runtimeToken)
	if err != nil {
		return cachedGatewayAccess{}, err
	}
	accessData := extractDataPayload(accessPayload)
	accessToken := extractString(accessData, "access_token")
	if accessToken == "" {
		return cachedGatewayAccess{}, errors.New("gateway exchange did not return access_token")
	}
	expiresAt, err := resolveExpiry(accessData, now)
	if err != nil {
		return cachedGatewayAccess{}, err
	}

	return cachedGatewayAccess{
		AccessToken: accessToken,
		GatewayBaseURL: strings.TrimRight(firstNonEmpty(
			explicitGatewayBaseURL(os.Getenv),
			extractString(accessData, "gateway_base_url", "base_url"),
		), "/"),
		InstanceID:  cfg.instanceID,
		ExpiresAtMs: expiresAt.UnixMilli(),
	}, nil
}

func explicitGatewayBaseURL(getenv func(string) string) string {
	if explicit := strings.TrimSpace(getenv(envBrowserBaseURL)); explicit != "" {
		return strings.TrimRight(explicit, "/")
	}
	if explicit := strings.TrimSpace(getenv(envRestBaseURL)); explicit != "" {
		return strings.TrimRight(deriveGatewayBaseURL(explicit), "/")
	}
	if explicit := strings.TrimSpace(getenv(envAPIBaseURL)); explicit != "" {
		return strings.TrimRight(deriveGatewayBaseURL(explicit), "/")
	}
	if explicit := strings.TrimSpace(getenv(envGraphQLBaseURL)); explicit != "" {
		return strings.TrimRight(deriveGatewayBaseURL(explicit), "/")
	}
	return ""
}

func postJSON(client *http.Client, endpoint string, payload any, bearerToken string) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exchange failed with %d", resp.StatusCode)
	}
	return parsed, nil
}

func extractDataPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	data, ok := payload["data"].(map[string]any)
	if ok {
		return data
	}
	return payload
}

func extractString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			switch typed := value.(type) {
			case string:
				if trimmed := strings.TrimSpace(typed); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

func resolveExpiry(payload map[string]any, now time.Time) (time.Time, error) {
	if expiresAt := extractString(payload, "expires_at"); expiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return time.Time{}, err
		}
		return parsed, nil
	}
	if raw, ok := payload["expires_in"]; ok {
		switch typed := raw.(type) {
		case float64:
			if typed > 0 {
				return now.Add(time.Duration(typed) * time.Second), nil
			}
		case int:
			if typed > 0 {
				return now.Add(time.Duration(typed) * time.Second), nil
			}
		}
	}
	return time.Time{}, errors.New("exchange response did not include an expiry")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}
