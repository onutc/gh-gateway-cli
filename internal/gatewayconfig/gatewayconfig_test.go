package gatewayconfig

import "testing"

func TestHostFromRESTBaseURL(t *testing.T) {
	t.Setenv(envRestBaseURL, "http://gateway.internal/api/v3/")

	if got := Host(); got != "gateway.internal" {
		t.Fatalf("Host() = %q", got)
	}
}

func TestIsReadOnlyTokenSource(t *testing.T) {
	if !IsReadOnlyTokenSource(envTokenCommand) {
		t.Fatalf("expected %q to be read-only", envTokenCommand)
	}
	if !IsReadOnlyTokenSource(envGatewayHelper) {
		t.Fatalf("expected %q to be read-only", envGatewayHelper)
	}
	if !IsReadOnlyTokenSource(envRuntimeExchangeURL) {
		t.Fatalf("expected %q to be read-only", envRuntimeExchangeURL)
	}
	if IsReadOnlyTokenSource("oauth_token") {
		t.Fatalf("expected oauth_token to remain writeable")
	}
}
