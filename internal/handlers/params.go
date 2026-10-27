package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/grafana/loki/v3/pkg/logcli/client"
	"github.com/spf13/viper"
)

// PingLoki reports whether Loki is reachable and the supplied credentials are
// accepted, by issuing a ListLabelNames call over the last minute and
// discarding the result. It returns nil only if Loki answered that call
// successfully; any transport error, non-2xx status or auth rejection comes
// back wrapped in an error naming params.URL.
//
// PingLoki reads only URL, Username, Password, Token and Org from params, and
// it does not call applyDefaults: the caller must supply a URL. An empty URL
// is a failure, not a fallback to localhost.
//
// An empty result is success. Loki having no labels at all is indistinguishable
// from Loki having labels, and that is deliberate — this is a liveness probe,
// not a data check.
//
// PingLoki bounds connection setup and response body reads by DefaultHTTPTimeout.
func PingLoki(params *LokiParams) error {
	return pingLokiWithTimeout(params, DefaultHTTPTimeout)
}

func pingLokiWithTimeout(params *LokiParams, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lokiClient := newLokiClient(params)
	// logcli explicitly disables http.Client.Timeout and exposes no context
	// argument. Attach the deadline to its request, keeping the original
	// transport, URL construction and authentication behavior.
	lokiClient.Tripperware = func(next http.RoundTripper) http.RoundTripper {
		return pingRoundTripper(func(req *http.Request) (*http.Response, error) {
			return next.RoundTrip(req.Clone(ctx))
		})
	}
	now := time.Now()
	_, err := lokiClient.ListLabelNames(true, now.Add(-time.Minute), now)
	if err != nil {
		// logcli replaces transport failures with an attempts-exhausted error.
		// Preserve the deadline cause so startup reports a useful timeout.
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("failed to reach Loki at %s: %w", params.URL, err)
	}
	return nil
}

// pingRoundTripper adapts a function to logcli's transport middleware.
type pingRoundTripper func(*http.Request) (*http.Response, error)

func (f pingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Defaults applied when a tool call leaves the corresponding argument unset.
// None of them is configurable at runtime; changing one changes the tool
// contract, so the tool descriptions in tools.go quote these values and must
// be updated with them.
const (
	// DefaultHTTPTimeout bounds a single LokiHTTPClient request end to end,
	// connection through body. It also bounds the startup PingLoki check.
	// The logcli client used for query_range sets no timeout.
	DefaultHTTPTimeout = 30 * time.Second
	// DefaultQueryLimit is the maximum number of entries requested from Loki
	// when the caller passes no positive limit. A zero or negative limit is
	// treated as absent, not as "no limit".
	DefaultQueryLimit = 100
	// DefaultLookback is the width of the window queried when the caller
	// supplies no start: start becomes now-DefaultLookback. It is only ever a
	// default for start. Supplying start alone widens or narrows the window
	// against a now end; supplying end alone still starts one hour before the
	// present moment, not one hour before end.
	DefaultLookback = 1 * time.Hour
)

// LokiParams is the union of the arguments of all three tools, bound from the
// MCP call's JSON arguments by (mcp.CallToolRequest).BindArguments.
//
// It is a union, not a per-tool struct, so several fields are meaningless for
// any given call and are simply ignored:
//
//   - Query is read by loki_query only. The label tools never look at it; a
//     query passed to them does not restrict their results.
//   - Label is read by loki_label_values only. It is required there and
//     ignored by the other two tools.
//   - Head, Tail, Filter, FilterCaseSensitive and Limit are read by
//     loki_query only. The label tools return whatever Loki returns.
//
// Limit, Head and Tail are float64 because MCP arguments are JSON and JSON has
// one number type. Each is converted with a plain int() conversion, which
// truncates toward zero: 2.7 means 2, 0.5 means 0 and therefore "unset", and
// -1.9 means -1 and therefore also "unset" because every use site tests > 0.
// This is intended, and is centralised at the three conversion sites rather
// than spread over the handler.
//
// The zero value is a valid starting point: (*LokiParams).applyDefaults fills
// URL, Username, Password, Token and Org from viper and Format from "raw",
// while Start and End are defaulted later by resolveTimeRange.
//
// Start and End are strings, not timestamps, and are parsed by parseTime:
// "now", a negative Go duration such as "-30m", RFC3339, or one of three
// zoneless layouts that are read as UTC. Empty means "use the default", which
// is now-DefaultLookback for Start and now for End — the two default
// independently, so supplying only End still starts one hour before the
// present moment rather than one hour before End.
type LokiParams struct {
	URL                 string  `json:"url"`
	Username            string  `json:"username"`
	Password            string  `json:"password"`
	Token               string  `json:"token"`
	Org                 string  `json:"org"`
	Start               string  `json:"start"`
	End                 string  `json:"end"`
	Format              string  `json:"format"`
	Limit               float64 `json:"limit"`                 // JSON numbers deserialize as float64
	Query               string  `json:"query"`                 // for loki_query
	Label               string  `json:"label"`                 // for loki_label_values
	Head                float64 `json:"head"`                  // Return only the first N entries (oldest)
	Tail                float64 `json:"tail"`                  // Return only the last N entries (newest)
	Filter              string  `json:"filter"`                // Filter log lines by substring
	FilterCaseSensitive bool    `json:"filter_case_sensitive"` // Case sensitivity for filter (default: false)
}

// applyDefaults fills in missing values from viper-bound config
func (p *LokiParams) applyDefaults() {
	if p.URL == "" {
		p.URL = viper.GetString("url")
	}
	if p.Username == "" {
		p.Username = viper.GetString("username")
	}
	if p.Password == "" {
		p.Password = viper.GetString("password")
	}
	if p.Token == "" {
		p.Token = viper.GetString("token")
	}
	if p.Org == "" {
		p.Org = viper.GetString("org-id")
	}
	if p.Format == "" {
		p.Format = "raw"
	}
}

// resolveTimeRange resolves the start/end window of a tool call. Both default
// to a DefaultLookback-wide window ending now; either can be overridden by the
// client. Every tool shares this rule, so it must change in one place.
func resolveTimeRange(params *LokiParams) (start, end time.Time, err error) {
	start = time.Now().Add(-DefaultLookback)
	end = time.Now()

	if params.Start != "" {
		startTime, err := parseTime(params.Start)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start time: %w", err)
		}
		start = startTime
	}

	if params.End != "" {
		endTime, err := parseTime(params.End)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end time: %w", err)
		}
		end = endTime
	}

	return start, end, nil
}

// newLokiClient creates a Loki client from the given parameters
func newLokiClient(params *LokiParams) *client.DefaultClient {
	return &client.DefaultClient{
		Address:     params.URL,
		Username:    params.Username,
		Password:    params.Password,
		BearerToken: params.Token,
		OrgID:       params.Org,
	}
}
