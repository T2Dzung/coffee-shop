package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

const defaultEnvironment = "development"

const redactedValue = "[REDACTED]"

type Config struct {
	Service     string
	Environment string
	Version     string
	Level       string
	Writer      io.Writer
}

func New(cfg Config) *slog.Logger {
	writer := cfg.Writer
	if writer == nil {
		writer = os.Stdout
	}
	service := strings.TrimSpace(cfg.Service)
	if service == "" {
		service = "unknown"
	}
	environment := strings.TrimSpace(cfg.Environment)
	if environment == "" {
		environment = defaultEnvironment
	}
	log := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level:       ParseLevel(cfg.Level),
		ReplaceAttr: redactSensitiveAttr,
	})).With(
		"service", service,
		"environment", environment,
	)
	if version := strings.TrimSpace(cfg.Version); version != "" {
		log = log.With("version", version)
	}
	return log
}

func redactSensitiveAttr(_ []string, attr slog.Attr) slog.Attr {
	switch strings.ToLower(attr.Key) {
	case "authorization", "connection_string", "dsn", "password", "secret", "token", "url":
		return slog.String(attr.Key, redactedValue)
	default:
		return attr
	}
}

func SetDefault(cfg Config) *slog.Logger {
	log := New(cfg)
	slog.SetDefault(log)
	return log
}

func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func Environment() string {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return defaultEnvironment
}
