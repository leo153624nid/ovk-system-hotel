// Package logx создаёт настроенный структурный логгер.
package logx

import (
	"log/slog"
	"os"
	"strings"
)

// New возвращает JSON-логгер с полем service и заданным уровнем (debug, info, warn, error).
func New(service, level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})

	return slog.New(h).With("service", service)
}
