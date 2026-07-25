package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

const testGenericAPIKey = "aZ9xY8wV7uT6sR5qP4nM3kL2jH1gF0dS" //gitleaks:allow — fake fixture for filter tests

// newTestConfig builds a handler Config, failing the test if it cannot.
//
// Tests that build a detector (secretFilter=true) must NOT call t.Parallel():
// betterleaks lazily initialises the process-global tiktoken BPE loader on the
// first scan, so two detectors scanning concurrently race inside the dependency.
// Production only ever builds one detector, so this is a test-only constraint.
func newTestConfig(t *testing.T, rawFilter string, secretFilter bool) Config {
	t.Helper()

	cfg, err := NewConfig(rawFilter, secretFilter)
	if err != nil {
		t.Fatalf("NewConfig(%q, %v) failed: %v", rawFilter, secretFilter, err)
	}
	return cfg
}

// newTestHandlers returns a handler set with no query filter and no secret
// filtering, matching the defaults most tests want.
func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()

	return New(newTestConfig(t, "", false))
}

func TestFilterSecretEntries_OmitsMatchingLines(t *testing.T) {
	cfg := newTestConfig(t, "", true)

	entries := []logEntry{
		{
			Timestamp: time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC),
			Line:      "normal application log",
			Labels:    loghttp.LabelSet{"job": "api"},
		},
		{
			Timestamp: time.Date(2026, 7, 7, 10, 0, 1, 0, time.UTC),
			Line:      `api_key = "` + testGenericAPIKey + `"`,
			Labels:    loghttp.LabelSet{"job": "api"},
		},
	}

	filtered, summary := filterSecretEntries(context.Background(), cfg.Detector, entries)

	if summary.LinesOmitted != 1 {
		t.Fatalf("expected 1 omitted line, got %d", summary.LinesOmitted)
	}
	if summary.Findings == 0 {
		t.Fatal("expected at least one secret finding")
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered entry, got %d", len(filtered))
	}
	if filtered[0].Line != "normal application log" {
		t.Fatalf("unexpected remaining line: %q", filtered[0].Line)
	}
}

func TestFilterSecretEntries_DisabledKeepsLines(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t, "", false)

	entries := []logEntry{{Line: `api_key = "` + testGenericAPIKey + `"`}}

	filtered, summary := filterSecretEntries(context.Background(), cfg.Detector, entries)

	if summary.LinesOmitted != 0 || summary.Findings != 0 {
		t.Fatalf("expected empty summary, got %+v", summary)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected disabled filter to keep the entry, got %d entries", len(filtered))
	}
}

// TestFilterSecretEntries_CancelledContext pins that a cancelled scan returns
// the entries filtered so far rather than the whole input.
func TestFilterSecretEntries_CancelledContext(t *testing.T) {
	cfg := newTestConfig(t, "", true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entries := []logEntry{{Line: "normal application log"}}

	filtered, summary := filterSecretEntries(ctx, cfg.Detector, entries)

	if len(filtered) != 0 {
		t.Fatalf("expected no entries from a cancelled scan, got %d", len(filtered))
	}
	if summary != (SecretFilterSummary{}) {
		t.Fatalf("expected empty summary, got %+v", summary)
	}
}

func TestFormatSecretOmissionNote(t *testing.T) {
	t.Parallel()

	if got := formatSecretOmissionNote(1); got != "1 line omitted due to detected secrets" {
		t.Fatalf("unexpected singular note: %q", got)
	}
	if got := formatSecretOmissionNote(3); got != "3 lines omitted due to detected secrets" {
		t.Fatalf("unexpected plural note: %q", got)
	}
}
