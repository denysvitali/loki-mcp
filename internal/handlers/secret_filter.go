package handlers

import (
	"context"
	"fmt"

	"github.com/betterleaks/betterleaks/detect"
)

// SecretFilterSummary describes content omitted from tool output.
type SecretFilterSummary struct {
	LinesOmitted int
	Findings     int
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

func formatSecretOmissionNote(lines int) string {
	if lines == 1 {
		return "1 line omitted due to detected secrets"
	}
	return fmt.Sprintf("%d lines omitted due to detected secrets", lines)
}
