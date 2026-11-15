package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// The subject of these tests is a package-level mutable logger, so every test
// in this file mutates shared state. None of them call t.Parallel(): they must
// run serially, and each one restores the pristine state captured by TestMain
// via restoreLogger. Making the logger injectable purely to enable parallel
// tests would be a production behaviour change, which this pass does not do.

// pristine holds the logger state built by init(), captured before any test
// has had a chance to mutate it. TestMain runs after all package-level
// variable initialisation and init() functions, and before any test, so this
// is the only reliable place to snapshot the shipped defaults.
var pristine struct {
	out       io.Writer
	level     logrus.Level
	formatter logrus.Formatter
}

func TestMain(m *testing.M) {
	pristine.out = logger.Out
	pristine.level = logger.Level
	pristine.formatter = logger.Formatter

	os.Exit(m.Run())
}

// restoreLogger returns the global logger to its post-init() state once the
// test finishes, so test order can never matter.
func restoreLogger(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		logger.SetOutput(pristine.out)
		logger.SetLevel(pristine.level)
		logger.SetFormatter(pristine.formatter)
		logger.ReplaceHooks(make(logrus.LevelHooks))
	})
}

// captureOutput redirects the global logger to a buffer for the duration of
// the test and returns it.
func captureOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	restoreLogger(t)
	buf := &bytes.Buffer{}
	logger.SetOutput(buf)
	return buf
}

const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

// TestDefaultOutputIsStderr pins the single most important property of this
// package. In stdio transport mode the MCP JSON-RPC stream owns os.Stdout;
// one log line written there corrupts the protocol. If this test fails,
// something redirected the default logger output and stdio mode is broken.
func TestDefaultOutputIsStderr(t *testing.T) {
	if pristine.out != io.Writer(os.Stderr) {
		t.Fatalf("default logger output = %#v, want os.Stderr (stdout belongs to the MCP protocol)", pristine.out)
	}
	if pristine.out == io.Writer(os.Stdout) {
		t.Fatal("default logger output is os.Stdout; this corrupts the stdio MCP protocol stream")
	}
}

// TestInitNeverChangesOutput pins that Init only touches level and formatter.
// Init must never call SetOutput, or a --log-format flag could silently move
// logs onto stdout.
func TestInitNeverChangesOutput(t *testing.T) {
	restoreLogger(t)

	sentinel := &bytes.Buffer{}
	for _, format := range []string{"text", "json", "", "unrecognised"} {
		for _, level := range []string{"debug", "info", "warn", "error"} {
			logger.SetOutput(sentinel)
			if err := Init(level, format); err != nil {
				t.Fatalf("Init(%q, %q) returned error: %v", level, format, err)
			}
			if logger.Out != io.Writer(sentinel) {
				t.Fatalf("Init(%q, %q) replaced the output writer: got %#v", level, format, logger.Out)
			}
		}
	}

	// And from the shipped default, the destination stays stderr.
	logger.SetOutput(pristine.out)
	if err := Init("debug", "json"); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if logger.Out != io.Writer(os.Stderr) {
		t.Fatalf("after Init the output is %#v, want os.Stderr", logger.Out)
	}
}

func TestDefaultLevelAndFormatterBeforeInit(t *testing.T) {
	if pristine.level != logrus.InfoLevel {
		t.Errorf("default level = %v, want %v", pristine.level, logrus.InfoLevel)
	}

	tf, ok := pristine.formatter.(*logrus.TextFormatter)
	if !ok {
		t.Fatalf("default formatter is %T, want *logrus.TextFormatter", pristine.formatter)
	}
	if !tf.FullTimestamp {
		t.Error("default TextFormatter.FullTimestamp = false, want true")
	}
	if tf.TimestampFormat != "" {
		t.Errorf("default TextFormatter.TimestampFormat = %q, want \"\" (logrus default)", tf.TimestampFormat)
	}
}

func TestInitSetsLevel(t *testing.T) {
	restoreLogger(t)

	tests := []struct {
		level string
		want  logrus.Level
	}{
		{"debug", logrus.DebugLevel},
		{"info", logrus.InfoLevel},
		{"warn", logrus.WarnLevel},
		{"error", logrus.ErrorLevel},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			if err := Init(tt.level, "text"); err != nil {
				t.Fatalf("Init(%q, \"text\") returned error: %v", tt.level, err)
			}
			if got := logger.GetLevel(); got != tt.want {
				t.Errorf("level = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInitSetsFormatter(t *testing.T) {
	restoreLogger(t)

	t.Run("json", func(t *testing.T) {
		if err := Init("info", "json"); err != nil {
			t.Fatalf("Init returned error: %v", err)
		}
		jf, ok := logger.Formatter.(*logrus.JSONFormatter)
		if !ok {
			t.Fatalf("formatter is %T, want *logrus.JSONFormatter", logger.Formatter)
		}
		if jf.TimestampFormat != timestampLayout {
			t.Errorf("TimestampFormat = %q, want %q", jf.TimestampFormat, timestampLayout)
		}
	})

	// Anything that is not "json" falls through to the text formatter; the
	// empty string and unknown values are not errors.
	for _, format := range []string{"text", "", "yaml", "JSON"} {
		t.Run("text/"+format, func(t *testing.T) {
			if err := Init("info", format); err != nil {
				t.Fatalf("Init(%q) returned error: %v", format, err)
			}
			tf, ok := logger.Formatter.(*logrus.TextFormatter)
			if !ok {
				t.Fatalf("formatter for %q is %T, want *logrus.TextFormatter", format, logger.Formatter)
			}
			if !tf.FullTimestamp {
				t.Error("TextFormatter.FullTimestamp = false, want true")
			}
			if tf.TimestampFormat != timestampLayout {
				t.Errorf("TimestampFormat = %q, want %q", tf.TimestampFormat, timestampLayout)
			}
		})
	}
}

func TestInitInvalidLevel(t *testing.T) {
	buf := captureOutput(t)

	// Establish a known state that the failing Init must not disturb.
	if err := Init("warn", "json"); err != nil {
		t.Fatalf("setup Init returned error: %v", err)
	}
	formatterBefore := logger.Formatter

	err := Init("verbose", "text")
	if err == nil {
		t.Fatal("Init with an invalid level returned nil error")
	}
	if !strings.Contains(err.Error(), "verbose") {
		t.Errorf("error %q does not mention the offending level", err)
	}

	// Level parsing happens first, so neither level nor formatter changes.
	if got := logger.GetLevel(); got != logrus.WarnLevel {
		t.Errorf("level after failed Init = %v, want %v (unchanged)", got, logrus.WarnLevel)
	}
	if logger.Formatter != formatterBefore {
		t.Errorf("formatter after failed Init = %T, want the previous *logrus.JSONFormatter", logger.Formatter)
	}

	// The logger is still usable.
	buf.Reset()
	Warn("still logging")
	if !strings.Contains(buf.String(), "still logging") {
		t.Errorf("logger unusable after a failed Init; output = %q", buf.String())
	}
}

func TestInitJSONEmitsParseableJSON(t *testing.T) {
	buf := captureOutput(t)

	if err := Init("info", "json"); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	Infof("hello %s", "world")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("json format did not emit parseable JSON: %v\noutput: %q", err, buf.String())
	}

	if got := record["msg"]; got != "hello world" {
		t.Errorf("msg = %v, want %q", got, "hello world")
	}
	if got := record["level"]; got != "info" {
		t.Errorf("level = %v, want %q", got, "info")
	}

	ts, ok := record["time"].(string)
	if !ok {
		t.Fatalf("time field is %T, want string", record["time"])
	}
	parsed, err := time.Parse(timestampLayout, ts)
	if err != nil {
		t.Fatalf("time %q does not match layout %q: %v", ts, timestampLayout, err)
	}
	if got := parsed.Format(timestampLayout); got != ts {
		t.Errorf("timestamp %q does not round-trip through layout %q (got %q)", ts, timestampLayout, got)
	}
}

func TestInitTextUsesTimestampLayout(t *testing.T) {
	buf := captureOutput(t)

	if err := Init("info", "text"); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	Info("plain message")

	line := buf.String()
	const prefix = `time="`
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("text output %q does not start with %s", line, prefix)
	}
	ts := line[len(prefix):]
	end := strings.Index(ts, `"`)
	if end < 0 {
		t.Fatalf("unterminated time field in %q", line)
	}
	ts = ts[:end]
	if _, err := time.Parse(timestampLayout, ts); err != nil {
		t.Errorf("text timestamp %q does not match layout %q: %v", ts, timestampLayout, err)
	}
	if !strings.Contains(line, "plain message") {
		t.Errorf("output %q does not contain the message", line)
	}
}

// captureHook records every entry it is fired with.
type captureHook struct {
	levels  []logrus.Level
	entries []*logrus.Entry
}

func (h *captureHook) Levels() []logrus.Level { return h.levels }

func (h *captureHook) Fire(entry *logrus.Entry) error {
	h.entries = append(h.entries, entry)
	return nil
}

func TestAddHookFires(t *testing.T) {
	buf := captureOutput(t)

	if err := Init("debug", "text"); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	hook := &captureHook{levels: logrus.AllLevels}
	AddHook(hook)

	Info("hooked message")
	Errorf("failure: %d", 42)

	if len(hook.entries) != 2 {
		t.Fatalf("hook fired %d times, want 2 (output was %q)", len(hook.entries), buf.String())
	}
	if got := hook.entries[0].Message; got != "hooked message" {
		t.Errorf("first entry message = %q, want %q", got, "hooked message")
	}
	if got := hook.entries[0].Level; got != logrus.InfoLevel {
		t.Errorf("first entry level = %v, want %v", got, logrus.InfoLevel)
	}
	if got := hook.entries[1].Message; got != "failure: 42" {
		t.Errorf("second entry message = %q, want %q", got, "failure: 42")
	}
	if got := hook.entries[1].Level; got != logrus.ErrorLevel {
		t.Errorf("second entry level = %v, want %v", got, logrus.ErrorLevel)
	}
}

// TestAddHookRespectsHookLevels pins that a hook is only fired for the levels
// it declares: the OpenTelemetry bridge relies on this.
func TestAddHookRespectsHookLevels(t *testing.T) {
	captureOutput(t)

	if err := Init("debug", "text"); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	hook := &captureHook{levels: []logrus.Level{logrus.ErrorLevel}}
	AddHook(hook)

	Info("ignored")
	Warn("also ignored")
	Errorf("captured")

	if len(hook.entries) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(hook.entries))
	}
	if got := hook.entries[0].Message; got != "captured" {
		t.Errorf("entry message = %q, want %q", got, "captured")
	}
}

func TestWrappersLogAtTheirLevel(t *testing.T) {
	tests := []struct {
		name      string
		log       func()
		wantLevel string
		wantMsg   string
	}{
		{"Info", func() { Info("a", "b") }, "info", "ab"},
		{"Infof", func() { Infof("n=%d", 1) }, "info", "n=1"},
		{"Warn", func() { Warn("careful") }, "warning", "careful"},
		{"Errorf", func() { Errorf("boom: %v", "x") }, "error", "boom: x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureOutput(t)
			if err := Init("debug", "json"); err != nil {
				t.Fatalf("Init returned error: %v", err)
			}

			tt.log()

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("unparseable output %q: %v", buf.String(), err)
			}
			if got := record["level"]; got != tt.wantLevel {
				t.Errorf("level = %v, want %q", got, tt.wantLevel)
			}
			if got := record["msg"]; got != tt.wantMsg {
				t.Errorf("msg = %v, want %q", got, tt.wantMsg)
			}
		})
	}
}

// TestLevelFiltersLowerSeverity pins that Init's level actually suppresses
// records, not just relabels them.
func TestLevelFiltersLowerSeverity(t *testing.T) {
	buf := captureOutput(t)

	if err := Init("error", "text"); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	Info("dropped")
	Warn("dropped too")
	if buf.Len() != 0 {
		t.Errorf("expected no output below error level, got %q", buf.String())
	}

	Errorf("kept")
	if !strings.Contains(buf.String(), "kept") {
		t.Errorf("error record was dropped; output = %q", buf.String())
	}
}
