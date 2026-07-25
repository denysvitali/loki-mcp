package handlers

import (
	"fmt"

	"github.com/betterleaks/betterleaks/detect"
	"github.com/prometheus/prometheus/model/labels"
)

// Config is the immutable, process-wide handler configuration built once at
// startup. Nothing writes to it after NewConfig returns.
type Config struct {
	// QueryFilter is ANDed into every client query. nil: queries are unrestricted.
	QueryFilter []*labels.Matcher
	// Detector scans returned log lines for secrets. nil: secret filtering disabled.
	Detector *detect.Detector
}

// NewConfig parses the raw LogQL stream selector and builds the secret detector.
// An empty rawFilter leaves queries unrestricted; secretFilter=false leaves the
// detector nil.
func NewConfig(rawFilter string, secretFilter bool) (Config, error) {
	var cfg Config

	if secretFilter {
		detector, err := detect.NewDetectorDefaultConfig()
		if err != nil {
			return Config{}, fmt.Errorf("initialize secret detector: %w", err)
		}
		detector.SkipFindingAppend = true
		cfg.Detector = detector
	}

	if rawFilter != "" {
		matchers, err := ParseFilter(rawFilter)
		if err != nil {
			return Config{}, err
		}
		cfg.QueryFilter = matchers
	}

	return cfg, nil
}

// Handlers serves the Loki MCP tools. Concurrency: cfg is read-only after
// construction, so all methods are safe to call from any goroutine.
type Handlers struct {
	cfg Config
}

// New returns a handler set bound to cfg.
func New(cfg Config) *Handlers {
	return &Handlers{cfg: cfg}
}
