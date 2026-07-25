package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// LokiHTTPClient talks to Loki's two label endpoints directly, over plain
// HTTP, because the logcli client this codebase otherwise uses cannot send the
// `query` parameter those endpoints accept. It exists for exactly that reason
// and is used only when a query filter is configured; it does not implement
// query_range and is not a general-purpose Loki client.
//
// Every request is bounded by DefaultHTTPTimeout (30s) end to end, including
// connection setup and reading the body, on top of whatever deadline the
// caller's context carries. Whichever fires first wins.
//
// A LokiHTTPClient is immutable after NewLokiHTTPClient and its methods are
// safe for concurrent use, as is the *http.Client inside it.
type LokiHTTPClient struct {
	baseURL     string
	username    string
	password    string
	bearerToken string
	orgID       string
	httpClient  *http.Client
}

// NewLokiHTTPClient returns a client that copies its address and credentials
// out of params; later changes to params do not affect it. It allocates a
// dedicated http.Client with a DefaultHTTPTimeout timeout, so clients are
// cheap but do not share a connection pool — one per request is the current
// usage and is intentional.
//
// params.URL is not validated here. A malformed URL surfaces as "invalid base
// URL" from the first method call, not from this constructor, which never
// fails and never touches the network. Credentials are optional: basic auth is
// sent only when both Username and Password are set, a bearer token only when
// Token is set, and X-Scope-OrgID only when Org is set. Setting both Password
// and Token sends both headers, with the bearer token overwriting the basic
// Authorization header.
func NewLokiHTTPClient(params *LokiParams) *LokiHTTPClient {
	return &LokiHTTPClient{
		baseURL:     params.URL,
		username:    params.Username,
		password:    params.Password,
		bearerToken: params.Token,
		orgID:       params.Org,
		httpClient: &http.Client{
			Timeout: DefaultHTTPTimeout,
		},
	}
}

// ListLabelNamesWithQuery GETs /loki/api/v1/labels and decodes the response.
// query is an optional LogQL stream selector restricting which labels are
// returned; "" omits the parameter entirely. start and end are sent as
// nanosecond Unix timestamps.
//
// Any status other than 200 is an error whose message includes the status code
// and the response body, so Loki's own explanation reaches the caller. A
// transport failure, a body that is not a LabelResponse, and a base URL that
// will not parse are likewise errors. On success the returned pointer is
// always non-nil, though its Data may be empty.
func (c *LokiHTTPClient) ListLabelNamesWithQuery(ctx context.Context, query string, start, end time.Time) (*loghttp.LabelResponse, error) {
	return c.fetchLabels(ctx, "/loki/api/v1/labels", query, start, end)
}

// ListLabelValuesWithQuery GETs /loki/api/v1/label/<name>/values for a single
// label. It has the same contract as ListLabelNamesWithQuery: optional query
// selector, nanosecond timestamps, and non-200 responses turned into errors
// carrying the status and the body.
//
// labelName is percent-escaped into the path and then escaped a second time
// when the URL is rendered, so a name containing a character that needs
// escaping is sent doubly encoded (FINDINGS.md §10). Real Loki label names are
// [a-zA-Z_][a-zA-Z0-9_]*, so nothing that needs escaping normally gets here.
func (c *LokiHTTPClient) ListLabelValuesWithQuery(ctx context.Context, labelName, query string, start, end time.Time) (*loghttp.LabelResponse, error) {
	path := fmt.Sprintf("/loki/api/v1/label/%s/values", url.PathEscape(labelName))
	return c.fetchLabels(ctx, path, query, start, end)
}

// fetchLabels GETs a Loki label endpoint and decodes the LabelResponse. query is
// the optional stream selector used to restrict the result; it is omitted when
// empty.
func (c *LokiHTTPClient) fetchLabels(ctx context.Context, path, query string, start, end time.Time) (*loghttp.LabelResponse, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	u.Path = path

	q := u.Query()
	q.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	q.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	if query != "" {
		q.Set("query", query)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result loghttp.LabelResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// addAuthHeaders sets basic auth (only when both username and password are
// present), the bearer token, and X-Scope-OrgID, skipping each when its value
// is empty. A bearer token overwrites the Authorization header basic auth set.
func (c *LokiHTTPClient) addAuthHeaders(req *http.Request) {
	if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
	if c.orgID != "" {
		req.Header.Set("X-Scope-OrgID", c.orgID)
	}
}
