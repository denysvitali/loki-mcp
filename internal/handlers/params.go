package handlers

import (
	"fmt"
	"time"

	"github.com/grafana/loki/v3/pkg/logcli/client"
	"github.com/spf13/viper"
)

// PingLoki verifies connectivity and authentication to Loki by making a
// lightweight ListLabelNames call via the SDK client.
func PingLoki(params *LokiParams) error {
	lokiClient := newLokiClient(params)
	now := time.Now()
	_, err := lokiClient.ListLabelNames(true, now.Add(-time.Minute), now)
	if err != nil {
		return fmt.Errorf("failed to reach Loki at %s: %w", params.URL, err)
	}
	return nil
}

// Default values
const (
	DefaultHTTPTimeout = 30 * time.Second
	DefaultQueryLimit  = 100
	DefaultLookback    = 1 * time.Hour
)

// LokiParams contains common parameters for Loki tool requests
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
