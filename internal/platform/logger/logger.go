// Package logger provides a zap-based structured logger.
package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config is the logger config. Kept minimal — full zap config is overkill here.
type Config struct {
	Level    string // debug, info, warn, error
	Encoding string // json, console
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

	zcfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         encoding,
		EncoderConfig:    encoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	l, err := zcfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}
	return l, nil
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
