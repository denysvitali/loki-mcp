package handlers

import (
	"fmt"
	"sync"

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
	// A non-nil Detector is the process-wide shared one (see sharedDetector);
	// it is read-only and must not be reconfigured through this field.
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
// secretFilter=true gives Config the process-wide betterleaks detector, built
// with the default ruleset and SkipFindingAppend set, so concurrent scans
// accumulate nothing in it. secretFilter=false leaves Detector nil.
// Constructing the detector compiles the full default ruleset and costs on the
// order of tens of milliseconds, which is why it happens once at startup and
// not per request.
//
// NewConfig is safe to call concurrently, and every caller that asks for
// secret filtering gets the *same* *detect.Detector — see sharedDetector for
// why there must only ever be one. Do not write to the returned
// Config.Detector: it is shared, and the whole Config is documented as
// immutable after construction.
func NewConfig(rawFilter string, secretFilter bool) (Config, error) {
	var cfg Config

	if secretFilter {
		detector, err := sharedDetector()
		if err != nil {
			return Config{}, err
		}
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

// sharedDetectorOnce guards sharedDetectorValue and sharedDetectorErr below.
// sync.Once rather than a lock because the value is built once and then only
// read, which is also what .golangci.yml's forbidigo rule against sync.Mutex
// asks for.
var (
	sharedDetectorOnce  sync.Once
	sharedDetectorValue *detect.Detector
	sharedDetectorErr   error
)

// sharedDetector returns the one betterleaks detector this process ever
// builds, constructing it on first use.
//
// There is deliberately only one. (*detect.Detector).Tokenizer() is guarded by
// a *per-detector* sync.Once whose body calls the *process-global*
// tiktoken.SetBpeLoader, an unsynchronised assignment to a package variable in
// github.com/pkoukk/tiktoken-go. Two detectors each run their own Once and
// both write that global, so two detectors scanning concurrently race inside
// the dependency (FINDINGS.md §11 — the real fix belongs upstream). With a
// single detector its own Once serialises the write and the hazard is gone;
// building it under this Once additionally makes concurrent NewConfig calls
// safe.
//
// The detector is immutable once built — SkipFindingAppend is set inside the
// Once, before any caller can see it — and betterleaks scans are safe to run
// concurrently against it, which is what makes sharing it sound.
//
// A construction failure is remembered: once it fails it fails identically for
// the rest of the process rather than being retried per call.
func sharedDetector() (*detect.Detector, error) {
	sharedDetectorOnce.Do(func() {
		d, err := detect.NewDetectorDefaultConfig()
		if err != nil {
			sharedDetectorErr = fmt.Errorf("initialize secret detector: %w", err)
			return
		}
		d.SkipFindingAppend = true
		sharedDetectorValue = d
	})
	return sharedDetectorValue, sharedDetectorErr
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
