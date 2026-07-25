package handlers

import (
	"fmt"

	"github.com/betterleaks/betterleaks/detect"
	"github.com/prometheus/prometheus/model/labels"
)

// Config is the immutable, process-wide handler configuration built once at
// startup. Nothing writes to it after NewConfig returns, so it may be copied
// freely and read from any goroutine.
//
// The zero Config is valid and means "unrestricted": every client query
// reaches Loki unmodified and no returned line is scanned for secrets.
type Config struct {
	// QueryFilter holds the matchers ANDed into every client query, prepended
	// to the matchers the client supplied. Empty or nil means queries are
	// unrestricted. For the two label tools a non-empty QueryFilter also
	// changes which client is used: only LokiHTTPClient can pass a selector
	// to Loki's label endpoints.
	QueryFilter []*labels.Matcher
	// Detector scans returned log lines for secrets; a line with any finding
	// is dropped from the response entirely. nil disables secret filtering,
	// and is the only way to disable it — there is no separate boolean.
	Detector *detect.Detector
}

// NewConfig builds the handler configuration from the two settings that
// control it. It returns the zero Config together with the error on any
// failure, so a partially built configuration is never handed back.
//
// rawFilter is a bare LogQL stream selector such as `{namespace="prod"}`.
// Empty leaves QueryFilter nil and queries unrestricted; anything else goes
// through ParseFilter, and an unparseable selector — or one carrying a
// pipeline, which ParseFilter refuses rather than silently dropping — is an
// error. A whitespace-only rawFilter also yields a nil QueryFilter, because
// ParseFilter trims first.
//
// secretFilter=true builds a betterleaks detector with the default ruleset and
// sets SkipFindingAppend, so concurrent scans accumulate nothing in the shared
// detector. secretFilter=false leaves Detector nil. Constructing the detector
// compiles the full default ruleset and costs on the order of tens of
// milliseconds, which is why it happens once at startup and not per request.
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

// Handlers is the set of three MCP tools this binary serves. It owns no
// mutable state: the only field is the Config it was constructed with, which
// is never written after construction, so every method is safe to call
// concurrently from any number of goroutines without a lock.
//
// A *Handlers is bound to one configuration for its whole life. To change the
// query filter or turn secret filtering on or off, build a new Config and a
// new Handlers; nothing here can be reconfigured in place.
type Handlers struct {
	cfg Config
}

// New returns a handler set bound to a copy of cfg. cfg is taken by value and
// the copy is never written, so later changes to the caller's cfg have no
// effect on the returned handlers.
//
// The zero Config is accepted and yields handlers that apply no query filter
// and no secret filtering. New never fails and never touches the network; the
// returned *Handlers is immediately usable.
func New(cfg Config) *Handlers {
	return &Handlers{cfg: cfg}
}
