// Package logger provides a zap-based structured logger.
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config is the logger config. Kept minimal — full zap config is overkill here.
type Config struct {
	Level    string // debug, info, warn, error
	Encoding string // json, console
	Output   string // comma-separated sinks: stdout, stderr, or a file path.
	// Defaults to stdout. An MCP stdio server MUST NOT include stdout: that
	// stream carries the JSON-RPC protocol, and any log line written there
	// corrupts it and breaks the client connection.
	//
	// A file sink is how the kb_query / llm_usage metric lines survive the
	// process — "stdout,log/kb.log" keeps both the console and the file.
	// Prefer a relative path: zap resolves sinks by URL scheme, so a Windows
	// absolute path like C:\logs\kb.log can be read as scheme "c".
	//
	// ponytail: no rotation — the file only grows. At the measured ~30KB/day
	// that is fine for years; reach for lumberjack if the volume changes.
}

// New constructs a zap.Logger. Caller is responsible for calling Sync() on
// shutdown.
func New(cfg Config) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	encoding := cfg.Encoding
	if encoding == "" {
		encoding = "json"
	}

	output := cfg.Output
	if output == "" {
		output = "stdout"
	}
	paths := splitOutputs(output)
	if err := ensureDirs(paths); err != nil {
		return nil, err
	}

	zcfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         encoding,
		EncoderConfig:    encoderConfig(),
		OutputPaths:      paths,
		ErrorOutputPaths: []string{"stderr"},
	}
	l, err := zcfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}
	return l, nil
}

func splitOutputs(output string) []string {
	paths := make([]string, 0, 2)
	for _, p := range strings.Split(output, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return []string{"stdout"}
	}
	return paths
}

// WithoutStdout drops stdout from an output spec and guarantees stderr, for
// callers whose stdout carries a protocol rather than text — the MCP stdio
// server. A single misconfigured LOG_OUTPUT would otherwise corrupt the
// JSON-RPC stream and break every client, so the rule lives here, tested,
// instead of being restated at the call site.
func WithoutStdout(output string) string {
	kept := make([]string, 0, 2)
	stderr := false
	for _, p := range splitOutputs(output) {
		switch p {
		case "stdout":
		case "stderr":
			stderr = true
			kept = append(kept, p)
		default:
			kept = append(kept, p)
		}
	}
	if !stderr {
		kept = append([]string{"stderr"}, kept...)
	}
	return strings.Join(kept, ",")
}

// ensureDirs creates the parent directory of every file sink. zap opens files
// but never creates directories, so a fresh checkout with log/ absent would
// fail at startup with an errno the caller cannot act on.
func ensureDirs(paths []string) error {
	for _, p := range paths {
		if p == "stdout" || p == "stderr" {
			continue
		}
		dir := filepath.Dir(p)
		if dir == "." {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create log dir %s: %w", dir, err)
		}
	}
	return nil
}

func parseLevel(s string) (zapcore.Level, error) {
	switch s {
	case "debug":
		return zapcore.DebugLevel, nil
	case "", "info":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return 0, fmt.Errorf("unknown log level: %s", s)
	}
}

func encoderConfig() zapcore.EncoderConfig {
	c := zap.NewProductionEncoderConfig()
	c.TimeKey = "ts"
	c.EncodeTime = zapcore.ISO8601TimeEncoder
	c.MessageKey = "msg"
	c.LevelKey = "level"
	c.CallerKey = "caller"
	c.EncodeLevel = zapcore.LowercaseLevelEncoder
	return c
}
