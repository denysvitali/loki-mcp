package handlers

import (
	"sync"
	"testing"

	"github.com/betterleaks/betterleaks/detect"
	"github.com/prometheus/prometheus/model/labels"
)

func TestParseFilter(t *testing.T) {
	tests := []struct {
		name      string
		filter    string
		wantCount int
		wantErr   bool
	}{
		{
			name:      "empty filter",
			filter:    "",
			wantCount: 0,
			wantErr:   false,
		},
		{
			name:      "single matcher",
			filter:    `{namespace="prod"}`,
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:      "multiple matchers",
			filter:    `{namespace="prod", job="api"}`,
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:      "regex matcher",
			filter:    `{job=~"api.*"}`,
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:      "not equal with positive matcher",
			filter:    `{namespace="prod", job!="test"}`,
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:      "not regex with positive matcher",
			filter:    `{namespace="prod", job!~"test.*"}`,
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:    "invalid filter - no braces",
			filter:  "namespace=prod",
			wantErr: true,
		},
		{
			name:    "invalid filter - bad syntax",
			filter:  `{namespace=}`,
			wantErr: true,
		},
		{
			name:      "whitespace around filter",
			filter:    `  {namespace="prod"}  `,
			wantCount: 1,
			wantErr:   false,
		},
		{
			// Whitespace-only is "unrestricted", not an error and not a
			// match-nothing filter.
			name:      "whitespace-only filter",
			filter:    "   \t\n ",
			wantCount: 0,
			wantErr:   false,
		},
		{
			// A pipeline stage cannot survive the trip into
			// Config.QueryFilter, which is a matcher slice, so ParseFilter
			// fails closed rather than enforcing the weaker stream selector
			// alone.
			name:    "line filter pipeline is rejected",
			filter:  `{namespace="prod"} |= "error"`,
			wantErr: true,
		},
		{
			name:    "parser stage is rejected",
			filter:  `{namespace="prod"} | json`,
			wantErr: true,
		},
		{
			name:    "multi-stage pipeline is rejected",
			filter:  `{namespace="prod"} |= "error" | json | level="error"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchers, err := ParseFilter(tt.filter)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFilter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(matchers) != tt.wantCount {
				t.Errorf("ParseFilter() got %d matchers, want %d", len(matchers), tt.wantCount)
			}
		})
	}
}

// TestParseFilter_EmptyIsUnrestricted pins that a blank filter is (nil, nil)
// rather than an error or an empty-but-non-nil slice: callers read a nil
// matcher slice as "no restriction".
func TestParseFilter_EmptyIsUnrestricted(t *testing.T) {
	t.Parallel()

	for _, filter := range []string{"", "   \t\n "} {
		matchers, err := ParseFilter(filter)
		if err != nil {
			t.Errorf("ParseFilter(%q) unexpected error: %v", filter, err)
		}
		if matchers != nil {
			t.Errorf("ParseFilter(%q) = %v, want nil", filter, matchers)
		}
	}
}

// TestParseFilter_RejectsPipeline pins the fail-closed error: it names the
// offending filter and the stages that could not be enforced, so an operator
// who writes a pipeline gets a startup failure instead of a silently weaker
// restriction.
func TestParseFilter_RejectsPipeline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		filter  string
		wantErr string
	}{
		{
			name:    "line filter",
			filter:  `{namespace="prod"} |= "error"`,
			wantErr: `invalid LogQL stream selector "{namespace=\"prod\"} |= \"error\"": only bare stream selectors are supported, but "|= \"error\"" cannot be enforced and would be silently discarded`,
		},
		{
			name:    "json parser",
			filter:  `{namespace="prod"} | json`,
			wantErr: `invalid LogQL stream selector "{namespace=\"prod\"} | json": only bare stream selectors are supported, but "| json" cannot be enforced and would be silently discarded`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			matchers, err := ParseFilter(tt.filter)
			if err == nil {
				t.Fatalf("ParseFilter(%q) = %v, want an error", tt.filter, matchers)
			}
			if matchers != nil {
				t.Errorf("ParseFilter(%q) returned %v alongside the error, want nil", tt.filter, matchers)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestMergeQueryWithFilter(t *testing.T) {
	// Create filter matchers
	filterMatchers := []*labels.Matcher{
		labels.MustNewMatcher(labels.MatchEqual, "namespace", "prod"),
	}

	tests := []struct {
		name    string
		query   string
		want    string
		wantErr bool
	}{
		{
			name:  "simple stream selector",
			query: `{job="api"}`,
			want:  `{namespace="prod", job="api"}`,
		},
		{
			name:  "stream selector with line filter",
			query: `{job="api"} |= "error"`,
			want:  `{namespace="prod", job="api"} |= "error"`,
		},
		{
			name:  "stream selector with json parser",
			query: `{job="api"} | json`,
			want:  `{namespace="prod", job="api"} | json`,
		},
		{
			name:  "stream selector with multiple pipeline stages",
			query: `{job="api"} |= "error" | json | level="error"`,
			want:  `{namespace="prod", job="api"} |= "error" | json | level="error"`,
		},
		{
			name:  "rate query",
			query: `rate({job="api"}[5m])`,
			want:  `rate({namespace="prod", job="api"}[5m])`,
		},
		{
			name:  "sum rate query",
			query: `sum(rate({job="api"}[5m]))`,
			want:  `sum(rate({namespace="prod", job="api"}[5m]))`,
		},
		{
			name:  "sum by rate query",
			query: `sum by (level) (rate({job="api"}[5m]))`,
			want:  `sum by (level)(rate({namespace="prod", job="api"}[5m]))`,
		},
		{
			name:  "count_over_time query",
			query: `count_over_time({job="api"}[1h])`,
			want:  `count_over_time({namespace="prod", job="api"}[1h])`,
		},
		{
			name:    "empty query",
			query:   "",
			wantErr: true,
		},
		{
			name:    "invalid query",
			query:   "not a valid query",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MergeQueryWithFilter(tt.query, filterMatchers)
			if (err != nil) != tt.wantErr {
				t.Errorf("MergeQueryWithFilter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("MergeQueryWithFilter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeQueryWithFilter_NoFilter(t *testing.T) {
	query := `{job="api"} |= "error"`
	got, err := MergeQueryWithFilter(query, nil)
	if err != nil {
		t.Errorf("MergeQueryWithFilter() unexpected error: %v", err)
		return
	}
	if got != query {
		t.Errorf("MergeQueryWithFilter() with no filter should return original query, got %q, want %q", got, query)
	}
}

func TestMergeQueryWithFilter_MultipleFilterMatchers(t *testing.T) {
	filterMatchers := []*labels.Matcher{
		labels.MustNewMatcher(labels.MatchEqual, "namespace", "prod"),
		labels.MustNewMatcher(labels.MatchEqual, "env", "us-west-2"),
	}

	query := `{job="api"}`
	got, err := MergeQueryWithFilter(query, filterMatchers)
	if err != nil {
		t.Errorf("MergeQueryWithFilter() unexpected error: %v", err)
		return
	}

	expected := `{namespace="prod", env="us-west-2", job="api"}`
	if got != expected {
		t.Errorf("MergeQueryWithFilter() = %q, want %q", got, expected)
	}
}

func TestBuildFilterSelector(t *testing.T) {
	tests := []struct {
		name     string
		matchers []*labels.Matcher
		want     string
	}{
		{
			name:     "empty matchers",
			matchers: nil,
			want:     "",
		},
		{
			name: "single matcher",
			matchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "namespace", "prod"),
			},
			want: `{namespace="prod"}`,
		},
		{
			name: "multiple matchers",
			matchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "namespace", "prod"),
				labels.MustNewMatcher(labels.MatchEqual, "job", "api"),
			},
			want: `{namespace="prod", job="api"}`,
		},
		{
			name: "regex matcher",
			matchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchRegexp, "job", "api.*"),
			},
			want: `{job=~"api.*"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildFilterSelector(tt.matchers)
			if got != tt.want {
				t.Errorf("BuildFilterSelector() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNewConfig covers the parsing of the raw query filter into matchers.
func TestNewConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		rawFilter    string
		wantMatchers int
		wantErr      bool
	}{
		{name: "valid selector", rawFilter: `{namespace="prod"}`, wantMatchers: 1},
		{name: "multiple matchers", rawFilter: `{namespace="prod", job="api"}`, wantMatchers: 2},
		{name: "empty filter leaves queries unrestricted", rawFilter: ""},
		{name: "invalid filter", rawFilter: "invalid filter", wantErr: true},
		{name: "pipeline filter is rejected", rawFilter: `{namespace="prod"} |= "error"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := NewConfig(tt.rawFilter, false)

			if tt.wantErr {
				if err == nil {
					t.Fatal("NewConfig() should have returned an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewConfig() unexpected error: %v", err)
			}
			if len(cfg.QueryFilter) != tt.wantMatchers {
				t.Errorf("QueryFilter length = %d, want %d", len(cfg.QueryFilter), tt.wantMatchers)
			}
			if cfg.Detector != nil {
				t.Error("Detector should be nil when secret filtering is disabled")
			}
		})
	}
}

// TestNewConfig_SecretFilter covers the detector half of the configuration.
func TestNewConfig_SecretFilter(t *testing.T) {
	cfg, err := NewConfig("", true)
	if err != nil {
		t.Fatalf("NewConfig() unexpected error: %v", err)
	}
	if cfg.Detector == nil {
		t.Fatal("Detector should be set when secret filtering is enabled")
	}
	if !cfg.Detector.SkipFindingAppend {
		t.Error("Detector should skip appending findings")
	}
}

// TestNewConfig_SecretDetectorIsShared pins that every Config gets the same
// detector and that concurrent NewConfig calls are race-free. Exactly one
// detector per process is what keeps the betterleaks/tiktoken global-write
// hazard out of reach — see config.go's sharedDetector for what that write is;
// run under -race to mean anything.
func TestNewConfig_SecretDetectorIsShared(t *testing.T) {
	const goroutines = 8

	detectors := make([]*detect.Detector, goroutines)
	var wg sync.WaitGroup
	for i := range detectors {
		wg.Add(1)
		go func() {
			defer wg.Done()

			cfg, err := NewConfig("", true)
			if err != nil {
				t.Errorf("NewConfig() unexpected error: %v", err)
				return
			}
			detectors[i] = cfg.Detector
		}()
	}
	wg.Wait()

	for i, d := range detectors {
		if d == nil {
			t.Fatalf("goroutine %d got a nil Detector", i)
		}
		if d != detectors[0] {
			t.Errorf("goroutine %d got detector %p, want the shared %p", i, d, detectors[0])
		}
	}
}
