package config

import (
	"testing"
	"time"
)

func TestString(t *testing.T) {
	t.Setenv("X_SET", "v")
	if got := String("X_SET", "def"); got != "v" {
		t.Fatalf("got %q, want v", got)
	}
	if got := String("X_UNSET_FOR_TEST", "def"); got != "def" {
		t.Fatalf("got %q, want def", got)
	}
}

func TestRequired(t *testing.T) {
	if _, err := Required("X_UNSET_FOR_TEST"); err == nil {
		t.Fatal("expected error for unset env")
	}
	t.Setenv("X_REQ", "v")
	if got, err := Required("X_REQ"); err != nil || got != "v" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestIntAndDuration(t *testing.T) {
	t.Setenv("X_INT", "42")
	if n, err := Int("X_INT", 1); err != nil || n != 42 {
		t.Fatalf("got %d, %v", n, err)
	}
	t.Setenv("X_INT", "abc")
	if _, err := Int("X_INT", 1); err == nil {
		t.Fatal("expected parse error")
	}
	t.Setenv("X_DUR", "15m")
	if d, err := Duration("X_DUR", time.Second); err != nil || d != 15*time.Minute {
		t.Fatalf("got %v, %v", d, err)
	}
}
