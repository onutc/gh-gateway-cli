package gatewayconfig

import (
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	envRestBaseURL    = "GH_REST_BASE_URL"
	envAPIBaseURL     = "GH_API_BASE_URL"
	envGraphQLBaseURL = "GH_GRAPHQL_BASE_URL"
	envBrowserBaseURL = "GH_BROWSER_BASE_URL"
	envTokenCommand   = "GH_TOKEN_COMMAND"
	envGatewayHelper  = "GH_GATEWAY_HELPER"
	envAuthScheme     = "GH_AUTH_SCHEME"

	defaultGatewayHelper = "gh-gateway-helper"
)

func trimmedEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func ensureTrailingSlash(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return parsed.String()
}

func deriveGatewayBaseURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	trimmedPath := strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/api/v3")
	trimmedPath = strings.TrimSuffix(trimmedPath, "/api/graphql")
	if trimmedPath == "" {
		parsed.Path = "/"
	} else {
		parsed.Path = trimmedPath + "/"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func appendAPISuffix(rawURL string, suffix string) string {
	baseURL := deriveGatewayBaseURL(rawURL)
	if baseURL == "" {
		return ""
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + suffix
	return parsed.String()
}

func helperExecutable() string {
	if explicit := strings.TrimSpace(os.Getenv(envGatewayHelper)); explicit != "" {
		return explicit
	}
	path, err := exec.LookPath(defaultGatewayHelper)
	if err != nil {
		return ""
	}
	return path
}

func shellProgram() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/s", "/c"}
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell, []string{"-c"}
	}
	return "sh", []string{"-c"}
}

func runShellCommand(command string) string {
	shell, args := shellProgram()
	cmd := exec.Command(shell, append(args, command)...)
	cmd.Env = os.Environ()
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func runHelperCommand(subcommand string) string {
	executable := helperExecutable()
	if executable == "" {
		return ""
	}
	cmd := exec.Command(executable, subcommand)
	cmd.Env = os.Environ()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func gatewayBaseURL() string {
	if explicit := trimmedEnv(envBrowserBaseURL); explicit != "" {
		return ensureTrailingSlash(explicit)
	}
	if explicit := trimmedEnv(envRestBaseURL, envAPIBaseURL); explicit != "" {
		return deriveGatewayBaseURL(explicit)
	}
	if explicit := trimmedEnv(envGraphQLBaseURL); explicit != "" {
		return deriveGatewayBaseURL(explicit)
	}
	if builtInBaseURL := builtInGatewayBaseURL(); builtInBaseURL != "" {
		return builtInBaseURL
	}
	if helperBase := runHelperCommand("gateway-base-url"); helperBase != "" {
		return ensureTrailingSlash(helperBase)
	}
	return ""
}

func hostFromRawURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Host)
}

func Host() string {
	if explicit := trimmedEnv(envRestBaseURL, envAPIBaseURL); explicit != "" {
		return hostFromRawURL(explicit)
	}
	if explicit := trimmedEnv(envGraphQLBaseURL); explicit != "" {
		return hostFromRawURL(explicit)
	}
	if explicit := trimmedEnv(envBrowserBaseURL); explicit != "" {
		return hostFromRawURL(explicit)
	}
	if baseURL := gatewayBaseURL(); baseURL != "" {
		return hostFromRawURL(baseURL)
	}
	return ""
}

func RESTBaseURL() string {
	if explicit := trimmedEnv(envRestBaseURL, envAPIBaseURL); explicit != "" {
		return ensureTrailingSlash(explicit)
	}
	if baseURL := gatewayBaseURL(); baseURL != "" {
		return appendAPISuffix(baseURL, "/api/v3/")
	}
	return ""
}

func GraphQLURL() string {
	if explicit := trimmedEnv(envGraphQLBaseURL); explicit != "" {
		return strings.TrimRight(explicit, "/")
	}
	if baseURL := gatewayBaseURL(); baseURL != "" {
		return appendAPISuffix(baseURL, "/api/graphql")
	}
	return ""
}

func BrowserBaseURL() string {
	return gatewayBaseURL()
}

func AuthorizationScheme() string {
	switch strings.ToLower(trimmedEnv(envAuthScheme)) {
	case "basic", "bearer", "token":
		return strings.ToLower(trimmedEnv(envAuthScheme))
	default:
		return "token"
	}
}

func ActiveToken() (string, string) {
	if command := trimmedEnv(envTokenCommand); command != "" {
		if token := runShellCommand(command); token != "" {
			return token, envTokenCommand
		}
	}
	if token, source := builtInActiveToken(); token != "" {
		return token, source
	}
	if token := runHelperCommand("token"); token != "" {
		return token, envGatewayHelper
	}
	return "", ""
}

func IsReadOnlyTokenSource(source string) bool {
	switch strings.TrimSpace(source) {
	case envTokenCommand, envGatewayHelper, envRuntimeExchangeURL:
		return true
	default:
		return false
	}
}
