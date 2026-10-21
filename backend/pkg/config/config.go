// Package config читает конфигурацию сервисов из переменных окружения.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// String возвращает значение переменной или def, если она не задана.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Required возвращает значение переменной или ошибку, если она не задана.
func Required(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required env %s is not set", key)
	}
	return v, nil
}

// Int читает целое число; пустое значение заменяется на def.
func Int(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: env %s: %w", key, err)
	}
	return n, nil
}

// Duration читает длительность в формате time.ParseDuration; пустое значение заменяется на def.
func Duration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: env %s: %w", key, err)
	}
	return d, nil
}
