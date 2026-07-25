package handlers

import (
	"strings"
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

// TestFilterEntries_CaseInsensitive tests case-insensitive filtering
func TestFilterEntries_CaseInsensitive(t *testing.T) {
	entries := []logEntry{
		{Timestamp: time.Now(), Line: "ERROR: something failed", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "info: everything is fine", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "Warning: potential issue", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "error in lowercase", Labels: loghttp.LabelSet{"job": "test"}},
	}

	// Case-insensitive search for "error"
	filtered := filterEntries(entries, "error", false)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 entries matching 'error' (case-insensitive), got %d", len(filtered))
	}

	// Verify both ERROR and error are matched
	for _, entry := range filtered {
		lowerLine := strings.ToLower(entry.Line)
		if !strings.Contains(lowerLine, "error") {
			t.Errorf("Entry '%s' should not have matched 'error'", entry.Line)
		}
	}
}

// TestFilterEntries_CaseSensitive tests case-sensitive filtering
func TestFilterEntries_CaseSensitive(t *testing.T) {
	entries := []logEntry{
		{Timestamp: time.Now(), Line: "ERROR: something failed", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "info: everything is fine", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "Warning: potential issue", Labels: loghttp.LabelSet{"job": "test"}},
		{Timestamp: time.Now(), Line: "error in lowercase", Labels: loghttp.LabelSet{"job": "test"}},
	}

	// Case-sensitive search for "ERROR"
	filtered := filterEntries(entries, "ERROR", true)

	if len(filtered) != 1 {
		t.Errorf("Expected 1 entry matching 'ERROR' (case-sensitive), got %d", len(filtered))
	}

	if len(filtered) > 0 && !strings.Contains(filtered[0].Line, "ERROR") {
		t.Errorf("Expected entry to contain 'ERROR', got '%s'", filtered[0].Line)
	}
}

// TestApplyHeadTail_Head tests head functionality
func TestApplyHeadTail_Head(t *testing.T) {
	entries := []logEntry{
		{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Line: "first"},
		{Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Line: "second"},
		{Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Line: "third"},
		{Timestamp: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC), Line: "fourth"},
		{Timestamp: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), Line: "fifth"},
	}

	result := applyHeadTail(entries, 3, 0)

	if len(result) != 3 {
		t.Errorf("Expected 3 entries with head=3, got %d", len(result))
	}

	expected := []string{"first", "second", "third"}
	for i, entry := range result {
		if entry.Line != expected[i] {
			t.Errorf("Entry %d: expected '%s', got '%s'", i, expected[i], entry.Line)
		}
	}
}

// TestApplyHeadTail_Tail tests tail functionality
func TestApplyHeadTail_Tail(t *testing.T) {
	entries := []logEntry{
		{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Line: "first"},
		{Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Line: "second"},
		{Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Line: "third"},
		{Timestamp: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC), Line: "fourth"},
		{Timestamp: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), Line: "fifth"},
	}

	result := applyHeadTail(entries, 0, 2)

	if len(result) != 2 {
		t.Errorf("Expected 2 entries with tail=2, got %d", len(result))
	}

	expected := []string{"fourth", "fifth"}
	for i, entry := range result {
		if entry.Line != expected[i] {
			t.Errorf("Entry %d: expected '%s', got '%s'", i, expected[i], entry.Line)
		}
	}
}

// TestApplyHeadTail_LargerThanCount tests head/tail when value exceeds entry count
func TestApplyHeadTail_LargerThanCount(t *testing.T) {
	entries := []logEntry{
		{Timestamp: time.Now(), Line: "first"},
		{Timestamp: time.Now(), Line: "second"},
	}

	// Head larger than count
	result := applyHeadTail(entries, 10, 0)
	if len(result) != 2 {
		t.Errorf("Expected all 2 entries when head > count, got %d", len(result))
	}

	// Tail larger than count
	result = applyHeadTail(entries, 0, 10)
	if len(result) != 2 {
		t.Errorf("Expected all 2 entries when tail > count, got %d", len(result))
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
