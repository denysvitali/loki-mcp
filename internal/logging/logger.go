package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

// logger is the process-wide logrus instance. It stays private: callers use
// the package-level wrappers below.
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

// Init initializes the logger with the given level and format
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

// AddHook adds a hook to the global logger.
func AddHook(hook logrus.Hook) {
	logger.AddHook(hook)
}

// Info logs an info message
func Info(args ...any) {
	logger.Info(args...)
}

// Infof logs a formatted info message
func Infof(format string, args ...any) {
	logger.Infof(format, args...)
}

// Warn logs a warning message
func Warn(args ...any) {
	logger.Warn(args...)
}

// Errorf logs a formatted error message
func Errorf(format string, args ...any) {
	logger.Errorf(format, args...)
}
