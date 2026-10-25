package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Plugin logging.
//
// A plugin's stderr is captured by the agent: go-plugin reads it line by
// line, and lines that are hclog-style JSON objects keep their level and
// fields; anything else is filed at debug. Log writes that JSON, so a
// plugin's messages arrive in the agent's per-plugin log ring (visible on
// the panel's plugin page, and in bug reports) with the level the plugin
// gave them.
//
// The level the agent wants to see is delivered twice: once as the
// EnvLogLevel variable when the subprocess starts, and again as gRPC
// metadata on every call, so a change made in the panel reaches a running
// plugin on its next call without a restart. SetLogLevel is the single
// switch both feed.
//
// At debug the SDK also records every external HTTP exchange the plugin
// makes through HTTPClient / LoggingTransport — URL, request and response —
// and every RPC the agent makes into the plugin. That is the level an
// operator turns on while validating an integration.

const (
	// EnvLogLevel is set on every plugin subprocess by the agent with the
	// level it wants to see: debug, info, warn, or error.
	EnvLogLevel = "RACKBILL_LOG_LEVEL"
	// MetadataLogLevel is the gRPC metadata key carrying the current level
	// on every call into the plugin.
	MetadataLogLevel = "rackbill-log-level"
)

// Log levels in the wire form the agent uses.
const (
	LogDebug = "debug"
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"
)

var (
	logLevel  slog.LevelVar // zero value is Info
	logMu     sync.Mutex
	logOutput io.Writer = os.Stderr

	// Log is the plugin's logger. Plugin authors log through it — or
	// through the standard library's log package, which serving redirects
	// here at info — and the agent files each record under the level it
	// carries. Records below the current level are dropped before
	// formatting, so debug calls are free when nobody asked for them.
	Log = slog.New(&stderrHandler{})
)

func init() {
	if lvl := os.Getenv(EnvLogLevel); lvl != "" {
		_ = SetLogLevel(lvl)
	}
}

// ParseLogLevel normalizes a level string ("warning" and "err" are accepted
// spellings) and reports whether it named one.
func ParseLogLevel(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace":
		return LogDebug, true
	case "info":
		return LogInfo, true
	case "warn", "warning":
		return LogWarn, true
	case "error", "err":
		return LogError, true
	}
	return "", false
}

// SetLogLevel changes the level Log emits at. Unknown strings are an error
// and leave the level alone.
func SetLogLevel(level string) error {
	lvl, ok := ParseLogLevel(level)
	if !ok {
		return fmt.Errorf("unknown log level %q (want debug, info, warn, or error)", level)
	}
	logLevel.Set(slogLevel(lvl))
	return nil
}

// LogLevel reports the current level in wire form.
func LogLevel() string { return levelName(logLevel.Level()) }

// DebugEnabled reports whether debug records are currently emitted — for
// callers that want to skip building an expensive message.
func DebugEnabled() bool { return logLevel.Level() <= slog.LevelDebug }

func slogLevel(level string) slog.Level {
	switch level {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return LogDebug
	case l < slog.LevelWarn:
		return LogInfo
	case l < slog.LevelError:
		return LogWarn
	default:
		return LogError
	}
}

// hclogTimeFormat is the @timestamp layout go-plugin's stderr parser
// insists on; anything else makes it file the whole line at debug.
const hclogTimeFormat = "2006-01-02T15:04:05.000000Z07:00"

// stderrHandler writes one hclog-compatible JSON object per record.
type stderrHandler struct {
	attrs  []slog.Attr // already qualified with the group they were added under
	prefix string      // dotted group path for record attrs, "" at top level
}

func (h *stderrHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= logLevel.Level()
}

func (h *stderrHandler) Handle(_ context.Context, r slog.Record) error {
	m := make(map[string]any, 3+len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		addAttr(m, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(m, h.prefix, a)
		return true
	})
	// The reserved keys win over any attribute that happens to share them.
	m["@level"] = levelName(r.Level)
	m["@message"] = r.Message
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	m["@timestamp"] = t.UTC().Format(hclogTimeFormat)
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	logMu.Lock()
	defer logMu.Unlock()
	_, err = logOutput.Write(b)
	return err
}

func (h *stderrHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	cp := &stderrHandler{prefix: h.prefix}
	cp.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range attrs {
		if h.prefix != "" {
			a = slog.Attr{Key: h.prefix + "." + a.Key, Value: a.Value}
		}
		cp.attrs = append(cp.attrs, a)
	}
	return cp
}

func (h *stderrHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	cp := &stderrHandler{attrs: h.attrs, prefix: name}
	if h.prefix != "" {
		cp.prefix = h.prefix + "." + name
	}
	return cp
}

func addAttr(m map[string]any, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	switch a.Value.Kind() {
	case slog.KindGroup:
		for _, g := range a.Value.Group() {
			addAttr(m, key, g)
		}
	case slog.KindString:
		m[key] = a.Value.String()
	case slog.KindInt64:
		m[key] = a.Value.Int64()
	case slog.KindUint64:
		m[key] = a.Value.Uint64()
	case slog.KindFloat64:
		m[key] = a.Value.Float64()
	case slog.KindBool:
		m[key] = a.Value.Bool()
	case slog.KindDuration:
		m[key] = a.Value.Duration().String()
	case slog.KindTime:
		m[key] = a.Value.Time().UTC().Format(time.RFC3339Nano)
	default:
		v := a.Value.Any()
		if err, ok := v.(error); ok {
			m[key] = err.Error()
		} else {
			m[key] = fmt.Sprint(v)
		}
	}
}

// stdLogWriter files the standard library's log output at info, so a plugin
// written with log.Printf keeps its messages once the agent is filtering
// stderr by level (go-plugin would otherwise file them at debug).
type stdLogWriter struct{}

func (stdLogWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			Log.Info(line)
		}
	}
	return len(p), nil
}

var redirectStdLogOnce sync.Once

func redirectStdLog() {
	redirectStdLogOnce.Do(func() {
		stdlog.SetFlags(0)
		stdlog.SetOutput(stdLogWriter{})
	})
}
