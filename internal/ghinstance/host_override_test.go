package ghinstance

import "testing"

func TestRESTPrefixHonorsEnvOverride(t *testing.T) {
	t.Setenv("GH_REST_BASE_URL", "http://gateway.example.test/api/v3/")

	if got := RESTPrefix("github.com"); got != "http://gateway.example.test/api/v3/" {
		t.Fatalf("RESTPrefix() = %q, want %q", got, "http://gateway.example.test/api/v3/")
	}
}

func TestGraphQLEndpointDerivesFromRESTOverride(t *testing.T) {
	t.Setenv("GH_REST_BASE_URL", "http://gateway.example.test/api/v3/")

	if got := GraphQLEndpoint("github.com"); got != "http://gateway.example.test/api/graphql" {
		t.Fatalf("GraphQLEndpoint() = %q, want %q", got, "http://gateway.example.test/api/graphql")
	}
}

func TestHostPrefixUsesBrowserOverride(t *testing.T) {
	t.Setenv("GH_BROWSER_BASE_URL", "http://gateway.example.test/")

	if got := HostPrefix("github.com"); got != "http://gateway.example.test/" {
		t.Fatalf("HostPrefix() = %q, want %q", got, "http://gateway.example.test/")
	}
}
