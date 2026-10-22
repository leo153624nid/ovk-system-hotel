package jwtx

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const secret = "0123456789abcdef0123456789abcdef"

func newTest(t *testing.T) *HS256 {
	t.Helper()

	h, err := NewHS256(secret, "iss")
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func TestSignVerifyRoundTrip(t *testing.T) {
	h := newTest(t)
	id := uuid.New()

	tok, err := h.Sign(id, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	got, err := h.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}

	if got.UserID != id {
		t.Fatalf("user id = %s, want %s", got.UserID, id)
	}
}

func TestVerifyRejects(t *testing.T) {
	h := newTest(t)
	id := uuid.New()

	valid, _ := h.Sign(id, time.Minute)
	expired, _ := h.Sign(id, -time.Minute)

	other, _ := NewHS256("fedcba9876543210fedcba9876543210", "iss")
	wrongKey, _ := other.Sign(id, time.Minute)

	otherIssuer, _ := NewHS256(secret, "other")
	wrongIssuer, _ := otherIssuer.Sign(id, time.Minute)

	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer: "iss", Subject: id.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	badSub, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "iss", Subject: "not-a-uuid", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}).SignedString([]byte(secret))

	noExp, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "iss", Subject: id.String(),
	}).SignedString([]byte(secret))

	cases := map[string]string{
		"empty":        "",
		"garbage":      "abc.def.ghi",
		"expired":      expired,
		"wrong key":    wrongKey,
		"wrong issuer": wrongIssuer,
		"alg none":     none,
		"bad subject":  badSub,
		"no expiry":    noExp,
		"tampered":     valid[:len(valid)-2] + "xx",
	}

	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.Verify(tok); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestNewHS256Validation(t *testing.T) {
	if _, err := NewHS256("short", "iss"); err == nil {
		t.Fatal("expected error for short secret")
	}
	if _, err := NewHS256(secret, ""); err == nil {
		t.Fatal("expected error for empty issuer")
	}
}
