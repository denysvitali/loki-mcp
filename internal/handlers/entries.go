package handlers

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// parseTime accepts, in order: the literal "now"; a negative Go duration such
// as "-1h" or "-30m", resolved against the present moment; RFC3339; and then
// "2006-01-02T15:04:05", "2006-01-02 15:04:05" and "2006-01-02".
//
// Only RFC3339 carries a zone. The three fallback layouts have none, so
// time.Parse reads them as UTC — a client that writes a local wall-clock time
// without an offset gets a UTC instant, silently shifted.
//
// A leading "-" that is not a valid duration is not an error; it falls through
// to the layout attempts. Anything none of them accepts returns "unsupported
// time format: <input>".
func parseTime(timeStr string) (time.Time, error) {
	// Handle "now" keyword
	if timeStr == "now" {
		return time.Now(), nil
	}

	// Handle relative time strings like "-1h", "-30m"
	if len(timeStr) > 0 && timeStr[0] == '-' {
		duration, err := time.ParseDuration(timeStr)
		if err == nil {
			return time.Now().Add(duration), nil
		}
	}

	// Try parsing as RFC3339
	t, err := time.Parse(time.RFC3339, timeStr)
	if err == nil {
		return t, nil
	}

	// Try other common formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		t, err := time.Parse(format, timeStr)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported time format: %s", timeStr)
}

// logEntry represents a single log entry with its associated labels for sorting and filtering
type logEntry struct {
	Timestamp time.Time
	Line      string
	Labels    loghttp.LabelSet
}

// collectAndSortEntries merges entries from all streams and sorts them newest
// first, matching the logproto.BACKWARD direction the range query asks Loki
// for. Post-processing therefore preserves the ordering a client would see if
// it were skipped entirely.
//
// sort.Slice is not stable, so entries sharing a timestamp are broken apart by
// line and then by label-set string. That makes the result a function of the
// input alone: equal-timestamp entries cannot reorder between two runs on the
// same data.
func collectAndSortEntries(streams loghttp.Streams) []logEntry {
	var entries []logEntry
	for _, stream := range streams {
		for _, entry := range stream.Entries {
			entries = append(entries, logEntry{
				Timestamp: entry.Timestamp,
				Line:      entry.Line,
				Labels:    stream.Labels,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.After(b.Timestamp)
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Labels.String() < b.Labels.String()
	})

	return entries
}

// filterEntries filters log entries by substring match
func filterEntries(entries []logEntry, filter string, caseSensitive bool) []logEntry {
	var filtered []logEntry
	searchFilter := filter
	if !caseSensitive {
		searchFilter = strings.ToLower(filter)
	}

	for _, entry := range entries {
		line := entry.Line
		if !caseSensitive {
			line = strings.ToLower(line)
		}
		if strings.Contains(line, searchFilter) {
			filtered = append(filtered, entry)
		}
	}

	return filtered
}

// applyHeadTail returns the head oldest or the tail newest entries, keeping
// the newest-first order collectAndSortEntries produced. Because that order is
// descending, head slices off the end and tail off the front — the mirror of
// what the names suggest. A count at or above len(entries), or a non-positive
// one, returns entries unchanged, and the result always aliases the input
// slice rather than copying.
//
// head wins if both are positive. The query handler rejects that combination
// before it gets here, so the precedence is a fallback, not a contract.
func applyHeadTail(entries []logEntry, head, tail int) []logEntry {
	if len(entries) == 0 {
		return entries
	}

	if head > 0 {
		if head >= len(entries) {
			return entries
		}
		return entries[len(entries)-head:]
	}

	if tail > 0 {
		if tail >= len(entries) {
			return entries
		}
		return entries[:tail]
	}

	return entries
}

// entriesToStreams converts a slice of logEntry back to loghttp.Streams
// format, grouping entries by their label set and preserving the entry order
// within each stream.
//
// The grouping runs through a map, whose iteration order Go randomises, so the
// streams are sorted by label-set string before they are returned. Without
// that, two identical queries could emit the same streams in different orders.
func entriesToStreams(entries []logEntry) loghttp.Streams {
	if len(entries) == 0 {
		return loghttp.Streams{}
	}

	// Group entries by labels
	streamMap := make(map[string]*loghttp.Stream)
	for _, entry := range entries {
		key := entry.Labels.String()
		if stream, exists := streamMap[key]; exists {
			stream.Entries = append(stream.Entries, loghttp.Entry{
				Timestamp: entry.Timestamp,
				Line:      entry.Line,
			})
		} else {
			streamMap[key] = &loghttp.Stream{
				Labels: entry.Labels,
				Entries: []loghttp.Entry{
					{
						Timestamp: entry.Timestamp,
						Line:      entry.Line,
					},
				},
			}
		}
	}

	keys := make([]string, 0, len(streamMap))
	for key := range streamMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	streams := make(loghttp.Streams, 0, len(keys))
	for _, key := range keys {
		streams = append(streams, *streamMap[key])
	}

	return streams
}
