package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	ghAPI "github.com/cli/go-gh/v2/pkg/api"
	ghauth "github.com/cli/go-gh/v2/pkg/auth"
	graphql "github.com/cli/shurcooL-graphql"

	"github.com/cli/cli/v2/internal/ghinstance"
)

const (
	apiVersion      = "X-GitHub-Api-Version"
	apiVersionValue = "2022-11-28"
	authorization   = "Authorization"
	cacheTTL        = "X-GH-CACHE-TTL"
	graphqlFeatures = "GraphQL-Features"
	features        = "merge_queue"
	userAgent       = "User-Agent"
)

var linkRE = regexp.MustCompile(`<([^>]+)>;\s*rel="([^"]+)"`)

func NewClientFromHTTP(httpClient *http.Client) *Client {
	client := &Client{http: httpClient}
	return client
}

type Client struct {
	http *http.Client
}

func (c *Client) HTTP() *http.Client {
	return c.http
}

type GraphQLError struct {
	*ghAPI.GraphQLError
}

type HTTPError struct {
	*ghAPI.HTTPError
	scopesSuggestion string
}

type graphQLHTTPResponse struct {
	Data   interface{}              `json:"data"`
	Errors []ghAPI.GraphQLErrorItem `json:"errors"`
}

func (err HTTPError) ScopesSuggestion() string {
	return err.scopesSuggestion
}

// GraphQL performs a GraphQL request using the query string and parses the response into data receiver. If there are errors in the response,
// GraphQLError will be returned, but the receiver will also be partially populated.
func (c Client) GraphQL(hostname string, query string, variables map[string]interface{}, data interface{}) error {
	endpoint := ghinstance.GraphQLEndpoint(ghauth.NormalizeHostname(hostname))
	reqBody, err := json.Marshal(map[string]interface{}{"query": query, "variables": variables})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set(graphqlFeatures, features)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !success {
		return HandleHTTPError(resp)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	gr := graphQLHTTPResponse{Data: data}
	if err := json.Unmarshal(body, &gr); err != nil {
		return err
	}

	if len(gr.Errors) > 0 {
		return GraphQLError{GraphQLError: &ghAPI.GraphQLError{Errors: gr.Errors}}
	}

	return nil
}

// Mutate performs a GraphQL mutation based on a struct and parses the response with the same struct as the receiver. If there are errors in the response,
// GraphQLError will be returned, but the receiver will also be partially populated.
func (c Client) Mutate(hostname, name string, mutation interface{}, variables map[string]interface{}) error {
	return c.MutateWithContext(context.Background(), hostname, name, mutation, variables)
}

// Query performs a GraphQL query based on a struct and parses the response with the same struct as the receiver. If there are errors in the response,
// GraphQLError will be returned, but the receiver will also be partially populated.
func (c Client) Query(hostname, name string, query interface{}, variables map[string]interface{}) error {
	return c.QueryWithContext(context.Background(), hostname, name, query, variables)
}

// QueryWithContext performs a GraphQL query based on a struct and parses the response with the same struct as the receiver. If there are errors in the response,
// GraphQLError will be returned, but the receiver will also be partially populated.
func (c Client) QueryWithContext(ctx context.Context, hostname, name string, query interface{}, variables map[string]interface{}) error {
	client := graphql.NewClient(ghinstance.GraphQLEndpoint(ghauth.NormalizeHostname(hostname)), c.graphQLHTTPClient())
	err := client.QueryNamed(ctx, name, query, variables)
	var graphQLErrs graphql.Errors
	if err != nil && errors.As(err, &graphQLErrs) {
		items := make([]ghAPI.GraphQLErrorItem, len(graphQLErrs))
		for i, e := range graphQLErrs {
			items[i] = ghAPI.GraphQLErrorItem{
				Message:    e.Message,
				Locations:  e.Locations,
				Path:       e.Path,
				Extensions: e.Extensions,
				Type:       e.Type,
			}
		}
		err = GraphQLError{GraphQLError: &ghAPI.GraphQLError{Errors: items}}
	}
	return err
}

// REST performs a REST request and parses the response.
func (c Client) REST(hostname string, method string, p string, body io.Reader, data interface{}) error {
	req, err := http.NewRequestWithContext(context.Background(), method, restURL(hostname, p), body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !success {
		defer resp.Body.Close()
		return HandleHTTPError(resp)
	}

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusResetContent {
		return nil
	}

	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return json.Unmarshal(payload, &data)
}

func (c Client) RESTWithNext(hostname string, method string, p string, body io.Reader, data interface{}) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, restURL(hostname, p), body)
	if err != nil {
		return "", err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !success {
		return "", HandleHTTPError(resp)
	}

	if resp.StatusCode == http.StatusNoContent {
		return "", nil
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	err = json.Unmarshal(b, &data)
	if err != nil {
		return "", err
	}

	var next string
	for _, m := range linkRE.FindAllStringSubmatch(resp.Header.Get("Link"), -1) {
		if len(m) > 2 && m[2] == "next" {
			next = m[1]
		}
	}

	return next, nil
}

// MutateWithContext executes a GraphQL mutation request.
func (c Client) MutateWithContext(ctx context.Context, hostname, name string, mutation interface{}, variables map[string]interface{}) error {
	client := graphql.NewClient(ghinstance.GraphQLEndpoint(ghauth.NormalizeHostname(hostname)), c.graphQLHTTPClient())
	err := client.MutateNamed(ctx, name, mutation, variables)
	var graphQLErrs graphql.Errors
	if err != nil && errors.As(err, &graphQLErrs) {
		items := make([]ghAPI.GraphQLErrorItem, len(graphQLErrs))
		for i, e := range graphQLErrs {
			items[i] = ghAPI.GraphQLErrorItem{
				Message:    e.Message,
				Locations:  e.Locations,
				Path:       e.Path,
				Extensions: e.Extensions,
				Type:       e.Type,
			}
		}
		err = GraphQLError{GraphQLError: &ghAPI.GraphQLError{Errors: items}}
	}
	return err
}

func restURL(hostname, pathOrURL string) string {
	if strings.HasPrefix(pathOrURL, "https://") || strings.HasPrefix(pathOrURL, "http://") {
		return pathOrURL
	}
	return ghinstance.RESTPrefix(ghauth.NormalizeHostname(hostname)) + pathOrURL
}

func (c Client) graphQLHTTPClient() *http.Client {
	baseTransport := c.http.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}

	client := *c.http
	client.Transport = funcTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
		if req.Header.Get(graphqlFeatures) == "" {
			req.Header.Set(graphqlFeatures, features)
		}
		return baseTransport.RoundTrip(req)
	}}

	return &client
}

// HandleHTTPError parses a http.Response into a HTTPError.
//
// The caller is responsible to close the response body stream.
func HandleHTTPError(resp *http.Response) error {
	return handleResponse(ghAPI.HandleHTTPError(resp))
}

// handleResponse takes a ghAPI.HTTPError or ghAPI.GraphQLError and converts it into an
// HTTPError or GraphQLError respectively.
func handleResponse(err error) error {
	if err == nil {
		return nil
	}

	var restErr *ghAPI.HTTPError
	if errors.As(err, &restErr) {
		return HTTPError{
			HTTPError: restErr,
			scopesSuggestion: generateScopesSuggestion(restErr.StatusCode,
				restErr.Headers.Get("X-Accepted-Oauth-Scopes"),
				restErr.Headers.Get("X-Oauth-Scopes"),
				restErr.RequestURL.Hostname()),
		}
	}

	var gqlErr *ghAPI.GraphQLError
	if errors.As(err, &gqlErr) {
		return GraphQLError{
			GraphQLError: gqlErr,
		}
	}

	return err
}

// ScopesSuggestion is an error messaging utility that prints the suggestion to request additional OAuth
// scopes in case a server response indicates that there are missing scopes.
func ScopesSuggestion(resp *http.Response) string {
	return generateScopesSuggestion(resp.StatusCode,
		resp.Header.Get("X-Accepted-Oauth-Scopes"),
		resp.Header.Get("X-Oauth-Scopes"),
		resp.Request.URL.Hostname())
}

// EndpointNeedsScopes adds additional OAuth scopes to an HTTP response as if they were returned from the
// server endpoint. This improves HTTP 4xx error messaging for endpoints that don't explicitly list the
// OAuth scopes they need.
func EndpointNeedsScopes(resp *http.Response, s string) {
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		oldScopes := resp.Header.Get("X-Accepted-Oauth-Scopes")
		resp.Header.Set("X-Accepted-Oauth-Scopes", fmt.Sprintf("%s, %s", oldScopes, s))
	}
}

func generateScopesSuggestion(statusCode int, endpointNeedsScopes, tokenHasScopes, hostname string) string {
	if statusCode < 400 || statusCode > 499 || statusCode == 422 {
		return ""
	}

	if tokenHasScopes == "" {
		return ""
	}

	gotScopes := map[string]struct{}{}
	for _, s := range strings.Split(tokenHasScopes, ",") {
		s = strings.TrimSpace(s)
		gotScopes[s] = struct{}{}

		// Certain scopes may be grouped under a single "top-level" scope. The following branch
		// statements include these grouped/implied scopes when the top-level scope is encountered.
		// See https://docs.github.com/en/developers/apps/building-oauth-apps/scopes-for-oauth-apps.
		if s == "repo" {
			gotScopes["repo:status"] = struct{}{}
			gotScopes["repo_deployment"] = struct{}{}
			gotScopes["public_repo"] = struct{}{}
			gotScopes["repo:invite"] = struct{}{}
			gotScopes["security_events"] = struct{}{}
		} else if s == "user" {
			gotScopes["read:user"] = struct{}{}
			gotScopes["user:email"] = struct{}{}
			gotScopes["user:follow"] = struct{}{}
		} else if s == "codespace" {
			gotScopes["codespace:secrets"] = struct{}{}
		} else if strings.HasPrefix(s, "admin:") {
			gotScopes["read:"+strings.TrimPrefix(s, "admin:")] = struct{}{}
			gotScopes["write:"+strings.TrimPrefix(s, "admin:")] = struct{}{}
		} else if strings.HasPrefix(s, "write:") {
			gotScopes["read:"+strings.TrimPrefix(s, "write:")] = struct{}{}
		}
	}

	for _, s := range strings.Split(endpointNeedsScopes, ",") {
		s = strings.TrimSpace(s)
		if _, gotScope := gotScopes[s]; s == "" || gotScope {
			continue
		}
		return fmt.Sprintf(
			"This API operation needs the %[1]q scope. To request it, run:  gh auth refresh -h %[2]s -s %[1]s",
			s,
			ghauth.NormalizeHostname(hostname),
		)
	}

	return ""
}
