package handlers

import (
	"context"
	"fmt"

	"github.com/betterleaks/betterleaks/detect"
)

// SecretFilterSummary counts what a secret scan removed. The zero value means
// nothing was removed, and is what callers get when secret filtering is off,
// when there was nothing to scan, or when every line was clean.
type SecretFilterSummary struct {
	// LinesOmitted is the number of log lines dropped from the response. It
	// is the number the user-facing note reports.
	LinesOmitted int
	// Findings is the total number of rule matches across those lines, so it
	// is >= LinesOmitted whenever LinesOmitted > 0: one line can trip several
	// rules. It feeds the metrics, not the note.
	Findings int
}

// filterSecretEntries drops every entry whose line matches a secret rule. A nil
// detector disables filtering. The scan is CPU-bound, so it polls ctx: a
// cancelled context returns the entries filtered so far.
func filterSecretEntries(ctx context.Context, detector *detect.Detector, entries []logEntry) ([]logEntry, SecretFilterSummary) {
	if detector == nil || len(entries) == 0 {
		return entries, SecretFilterSummary{}
	}

	filtered := make([]logEntry, 0, len(entries))
	var summary SecretFilterSummary
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return filtered, summary
		default:
		}

		findings := detector.DetectString(entry.Line)
		if len(findings) > 0 {
			summary.LinesOmitted++
			summary.Findings += len(findings)
			continue
		}
		filtered = append(filtered, entry)
	}

	return filtered, summary
}

// formatSecretOmissionNote renders the user-facing count, singular for one
// line. The wording is asserted verbatim by tests and appears both in rendered
// raw/text output and in the JSON response's Warnings array.
func formatSecretOmissionNote(lines int) string {
	if lines == 1 {
		return "1 line omitted due to detected secrets"
	}
	return fmt.Sprintf("%d lines omitted due to detected secrets", lines)
}
