// Package logging owns the single logrus logger shared by every package in
// this binary.
//
// # All output goes to stderr
//
// The logger writes to os.Stderr and nothing in this package ever moves it.
// In stdio transport mode the MCP JSON-RPC stream owns os.Stdout, so a single
// log line written there corrupts the protocol and the client disconnects.
// Init therefore adjusts only the level and the formatter; it deliberately
// never calls SetOutput. Any future change here must keep the destination on
// stderr — logger_test.go fails loudly if it does not.
//
// # What belongs here
//
// Logger construction and configuration only: the level/format wiring behind
// the --log-level and --log-format flags, hook registration (used to bridge
// records into OpenTelemetry), and the leveled wrappers callers use instead
// of holding a logger value. Deciding what to log, and with which fields,
// belongs in the calling package.
//
// # Concurrency
//
// A *logrus.Logger is safe for concurrent use, so the leveled wrappers may be
// called from any goroutine. The package-level logger is built once by init
// and afterwards mutated only by Init and AddHook, both of which are expected
// to run during startup, before any goroutine that logs is started. The
// logger variable itself is never reassigned after init.
package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

// logger is the process-wide logrus instance. It stays private: callers use
// the package-level wrappers below, which guarantees there is exactly one
// output destination to reason about.
var logger *logrus.Logger

func init() {
	logger = logrus.New()
	// Default to stderr - critical for stdio mode where stdout is MCP protocol
	logger.SetOutput(os.Stderr)
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
}

// Init applies the configured level and output format to the process-wide
// logger. It is meant to be called once, from startup, before anything logs.
//
// Guarantees:
//   - The output destination is never touched: logs keep going to stderr.
//   - level is parsed by logrus.ParseLevel ("debug", "info", "warn",
//     "error", ...). On an unparseable level Init returns that error and
//     changes nothing at all — neither the level nor the formatter — so the
//     caller can report the error and keep using the logger.
//   - format "json" selects logrus.JSONFormatter. Every other value,
//     including the empty string and typos, selects the text formatter; an
//     unknown format is not an error.
//   - Both formatters timestamp with layout 2006-01-02T15:04:05.000Z07:00.
func Init(level, format string) error {
	// Parse and set log level
	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}
	logger.SetLevel(lvl)

	// Set formatter
	switch format {
	case "json":
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	default:
		logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	}

	return nil
}

// AddHook registers hook on the process-wide logger.
//
// The hook fires synchronously for every subsequent record whose level is
// listed in hook.Levels(), in addition to the record being written to stderr;
// records emitted before the call are not replayed. Hooks cannot be removed,
// so register them once at startup. A hook that blocks blocks the caller of
// the log statement.
func AddHook(hook logrus.Hook) {
	logger.AddHook(hook)
}

// Info writes args to stderr at info level, formatted like fmt.Sprint.
// The record is dropped if the configured level is above info.
func Info(args ...any) {
	logger.Info(args...)
}

// Infof writes a fmt.Sprintf-formatted record to stderr at info level.
// The record is dropped if the configured level is above info.
func Infof(format string, args ...any) {
	logger.Infof(format, args...)
}

// Warn writes args to stderr at warning level, formatted like fmt.Sprint.
// The record is dropped if the configured level is above warning.
func Warn(args ...any) {
	logger.Warn(args...)
}

// Errorf writes a fmt.Sprintf-formatted record to stderr at error level.
// It reports a problem; it does not terminate the process.
func Errorf(format string, args ...any) {
	logger.Errorf(format, args...)
}
