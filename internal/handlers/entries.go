package handlers

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// parseTime parses a time string in various formats
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

// collectAndSortEntries merges entries from all streams and sorts them by timestamp (oldest first)
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

	// Sort by timestamp ascending (oldest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
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

// applyHeadTail returns the first N entries (head) or last N entries (tail)
func applyHeadTail(entries []logEntry, head, tail int) []logEntry {
	if len(entries) == 0 {
		return entries
	}

	if head > 0 {
		if head >= len(entries) {
			return entries
		}
		return entries[:head]
	}

	if tail > 0 {
		if tail >= len(entries) {
			return entries
		}
		return entries[len(entries)-tail:]
	}

	return entries
}

// entriesToStreams converts a slice of logEntry back to loghttp.Streams format
// Entries are grouped by their labels
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

	// Convert map to slice
	var streams loghttp.Streams
	for _, stream := range streamMap {
		streams = append(streams, *stream)
	}

	return streams
}
