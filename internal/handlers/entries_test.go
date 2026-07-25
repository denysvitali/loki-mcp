package handlers

import (
	"testing"
	"time"
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
