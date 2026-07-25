package handlers

import (
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// TestParseTime pins the accepted time formats, including the quirk that a
// leading "-" which fails time.ParseDuration falls through to the absolute
// format parsers rather than surfacing the duration error.
func TestParseTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		want     time.Time
		relative time.Duration // used when want is zero and wantErr is false
		useRel   bool
		wantErr  string
	}{
		{name: "now keyword", input: "now", useRel: true, relative: 0},
		{name: "relative hours", input: "-1h", useRel: true, relative: -time.Hour},
		{name: "relative minutes", input: "-30m", useRel: true, relative: -30 * time.Minute},
		{name: "positive duration is not relative", input: "1h", wantErr: "unsupported time format: 1h"},
		{
			name:  "rfc3339",
			input: "2024-01-15T10:30:45Z",
			want:  time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
		},
		{
			name:  "rfc3339 with offset",
			input: "2024-01-15T10:30:45+02:00",
			want:  time.Date(2024, 1, 15, 10, 30, 45, 0, time.FixedZone("", 2*60*60)),
		},
		{
			name:  "datetime without zone",
			input: "2024-01-15T10:30:45",
			want:  time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
		},
		{
			name:  "space separated datetime",
			input: "2024-01-15 10:30:45",
			want:  time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
		},
		{
			name:  "date only",
			input: "2024-01-15",
			want:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{name: "garbage", input: "not a time", wantErr: "unsupported time format: not a time"},
		{name: "empty", input: "", wantErr: "unsupported time format: "},
		// Quirk: the leading "-" branch swallows the ParseDuration error and
		// keeps going, so the reported error is the generic one.
		{name: "bad duration falls through", input: "-1x", wantErr: "unsupported time format: -1x"},
		{name: "negative garbage falls through", input: "-nope", wantErr: "unsupported time format: -nope"},
		{name: "negative date falls through", input: "-2024-01-15", wantErr: "unsupported time format: -2024-01-15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := time.Now()
			got, err := parseTime(tt.input)
			after := time.Now()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseTime(%q) = %v, want error %q", tt.input, got, tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("parseTime(%q) error = %q, want %q", tt.input, err.Error(), tt.wantErr)
				}
				if !got.IsZero() {
					t.Errorf("parseTime(%q) returned %v on error, want zero time", tt.input, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseTime(%q) unexpected error: %v", tt.input, err)
			}

			if tt.useRel {
				lo := before.Add(tt.relative)
				hi := after.Add(tt.relative)
				if got.Before(lo) || got.After(hi) {
					t.Errorf("parseTime(%q) = %v, want within [%v, %v]", tt.input, got, lo, hi)
				}
				return
			}

			if !got.Equal(tt.want) {
				t.Errorf("parseTime(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestFilterEntries covers case-insensitive and case-sensitive substring
// filtering in one table.
func TestFilterEntries(t *testing.T) {
	t.Parallel()

	entries := []logEntry{
		{Timestamp: time.Now(), Line: "ERROR: something failed", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "info: everything is fine", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "Warning: potential issue", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "error in lowercase", Labels: loghttp.LabelSet{"job": "test"}},
	}

	tests := []struct {
		name          string
		filter        string
		caseSensitive bool
		want          []string
	}{
		{
			name:   "case-insensitive matches both cases",
			filter: "error",
			want:   []string{"ERROR: something failed", "error in lowercase"},
		},
		{
			name:          "case-sensitive matches only the exact case",
			filter:        "ERROR",
			caseSensitive: true,
			want:          []string{"ERROR: something failed"},
		},
		{
			name:   "no match yields nothing",
			filter: "no-such-substring",
		},
		{
			name:   "empty filter matches everything",
			filter: "",
			want: []string{
				"ERROR: something failed",
				"info: everything is fine",
				"Warning: potential issue",
				"error in lowercase",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			filtered := filterEntries(entries, tt.filter, tt.caseSensitive)

			if len(filtered) != len(tt.want) {
				t.Fatalf("got %d entries, want %d", len(filtered), len(tt.want))
			}
			for i, entry := range filtered {
				if entry.Line != tt.want[i] {
					t.Errorf("entry %d = %q, want %q", i, entry.Line, tt.want[i])
				}
			}
		})
	}
}

// TestApplyHeadTail covers head, tail, and the out-of-range cases in one table.
func TestApplyHeadTail(t *testing.T) {
	t.Parallel()

	five := []logEntry{
		{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Line: "first"},
		{Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Line: "second"},
		{Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Line: "third"},
		{Timestamp: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC), Line: "fourth"},
		{Timestamp: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), Line: "fifth"},
	}

	tests := []struct {
		name    string
		entries []logEntry
		head    int
		tail    int
		want    []string
	}{
		{name: "head takes the oldest", entries: five, head: 3, want: []string{"first", "second", "third"}},
		{name: "tail takes the newest", entries: five, tail: 2, want: []string{"fourth", "fifth"}},
		{
			name: "head larger than count returns everything", entries: five, head: 10,
			want: []string{"first", "second", "third", "fourth", "fifth"},
		},
		{
			name: "tail larger than count returns everything", entries: five, tail: 10,
			want: []string{"first", "second", "third", "fourth", "fifth"},
		},
		{
			name: "neither head nor tail returns everything", entries: five,
			want: []string{"first", "second", "third", "fourth", "fifth"},
		},
		{name: "empty input", entries: nil, head: 3},
		{name: "head wins when both are set", entries: five, head: 1, tail: 1, want: []string{"first"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := applyHeadTail(tt.entries, tt.head, tt.tail)

			if len(result) != len(tt.want) {
				t.Fatalf("got %d entries, want %d", len(result), len(tt.want))
			}
			for i, entry := range result {
				if entry.Line != tt.want[i] {
					t.Errorf("entry %d = %q, want %q", i, entry.Line, tt.want[i])
				}
			}
		})
	}
}

// TestCollectAndSortEntries_MultipleStreams tests merging and sorting across streams
func TestCollectAndSortEntries_MultipleStreams(t *testing.T) {
	streams := loghttp.Streams{
		{
			Labels: loghttp.LabelSet{"job": "job1"},
			Entries: []loghttp.Entry{
				{Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Line: "job1-third"},
				{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Line: "job1-first"},
			},
		},
		{
			Labels: loghttp.LabelSet{"job": "job2"},
			Entries: []loghttp.Entry{
				{Timestamp: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC), Line: "job2-fourth"},
				{Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Line: "job2-second"},
			},
		},
	}

	entries := collectAndSortEntries(streams)

	if len(entries) != 4 {
		t.Fatalf("Expected 4 entries, got %d", len(entries))
	}

	// Verify entries are sorted by timestamp (oldest first)
	expected := []string{"job1-first", "job2-second", "job1-third", "job2-fourth"}
	for i, entry := range entries {
		if entry.Line != expected[i] {
			t.Errorf("Entry %d: expected '%s', got '%s'", i, expected[i], entry.Line)
		}
	}
}

// TestEntriesToStreams tests converting entries back to streams format
func TestEntriesToStreams(t *testing.T) {
	entries := []logEntry{
		{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Line: "first", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Line: "second", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Line: "third", Labels: loghttp.LabelSet{"job": "other"}},
	}

	streams := entriesToStreams(entries)

	// Should have 2 streams (grouped by labels)
	if len(streams) != 2 {
		t.Errorf("Expected 2 streams, got %d", len(streams))
	}

	// Count total entries across streams
	totalEntries := 0
	for _, stream := range streams {
		totalEntries += len(stream.Entries)
	}
	if totalEntries != 3 {
		t.Errorf("Expected 3 total entries across streams, got %d", totalEntries)
	}
}

// TestEntriesToStreams_Empty tests converting empty entries
func TestEntriesToStreams_Empty(t *testing.T) {
	streams := entriesToStreams([]logEntry{})

	if len(streams) != 0 {
		t.Errorf("Expected 0 streams for empty entries, got %d", len(streams))
	}
}
