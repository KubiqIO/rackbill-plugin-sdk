package plugin

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captureLog swaps the log output for a buffer for one test and restores
// the level afterwards.
func captureLog(t *testing.T, level string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logOutput
	prevLevel := LogLevel()
	logMu.Lock()
	logOutput = &buf
	logMu.Unlock()
	if err := SetLogLevel(level); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		logMu.Lock()
		logOutput = prev
		logMu.Unlock()
		_ = SetLogLevel(prevLevel)
	})
	return &buf
}

// parseHclogLine mirrors what go-plugin's stderr reader does with a line:
// it must be a JSON object whose @level and @message are strings and whose
// @timestamp parses with hclog's exact layout — otherwise the whole line is
// filed at debug verbatim.
func parseHclogLine(t *testing.T, line string) (level, msg string, kv map[string]any) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatalf("line is not JSON: %v: %s", err, line)
	}
	level, _ = raw["@level"].(string)
	msg, _ = raw["@message"].(string)
	ts, _ := raw["@timestamp"].(string)
	if _, err := time.Parse("2006-01-02T15:04:05.000000Z07:00", ts); err != nil {
		t.Fatalf("@timestamp %q does not parse with go-plugin's layout: %v", ts, err)
	}
	delete(raw, "@level")
	delete(raw, "@message")
	delete(raw, "@timestamp")
	return level, msg, raw
}

func TestLogWritesHclogJSON(t *testing.T) {
	buf := captureLog(t, LogInfo)
	Log.Debug("hidden")
	Log.With("plugin", "x").WithGroup("http").Info("hello", "status", 200, "ok", true, "err", errString("boom"))
	Log.Warn("careful")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	level, msg, kv := parseHclogLine(t, lines[0])
	if level != "info" || msg != "hello" {
		t.Fatalf("level/msg = %q/%q", level, msg)
	}
	if kv["plugin"] != "x" || kv["http.status"] != float64(200) || kv["http.ok"] != true || kv["http.err"] != "boom" {
		t.Fatalf("kv = %v", kv)
	}
	if level, _, _ := parseHclogLine(t, lines[1]); level != "warn" {
		t.Fatalf("second level = %q", level)
	}
}

func TestSetLogLevel(t *testing.T) {
	captureLog(t, LogInfo)
	if DebugEnabled() {
		t.Fatal("debug enabled at info")
	}
	if err := SetLogLevel("DEBUG"); err != nil || LogLevel() != LogDebug || !DebugEnabled() {
		t.Fatalf("debug: err=%v level=%s", err, LogLevel())
	}
	if err := SetLogLevel("warning"); err != nil || LogLevel() != LogWarn {
		t.Fatalf("warning: err=%v level=%s", err, LogLevel())
	}
	if err := SetLogLevel("loud"); err == nil || LogLevel() != LogWarn {
		t.Fatalf("unknown level accepted: err=%v level=%s", err, LogLevel())
	}
	if !Log.Enabled(nil, slog.LevelError) || Log.Enabled(nil, slog.LevelInfo) {
		t.Fatal("handler Enabled disagrees with the level")
	}
}

func TestStdLogRedirect(t *testing.T) {
	buf := captureLog(t, LogInfo)
	redirectStdLog()
	stdLogWriter{}.Write([]byte("legacy line\n"))
	level, msg, _ := parseHclogLine(t, strings.TrimSpace(buf.String()))
	if level != "info" || msg != "legacy line" {
		t.Fatalf("std log line = %q/%q", level, msg)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
