package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/auth/internal/domain"
)

type fakeStore struct {
	mu      sync.Mutex
	users   map[string]domain.User
	refresh map[string]domain.RefreshToken
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:   map[string]domain.User{},
		refresh: map[string]domain.RefreshToken{},
	}
}

func (f *fakeStore) CreateUser(_ context.Context, u domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.users[u.Email]; ok {
		return domain.ErrEmailTaken
	}

	f.users[u.Email] = u

	return nil
}

func (f *fakeStore) UserByEmail(_ context.Context, email string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.users[email]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}

	return u, nil
}

func (f *fakeStore) SaveRefresh(_ context.Context, t domain.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.refresh[string(t.Hash)] = t

	return nil
}

func (f *fakeStore) RotateRefresh(context.Context, []byte, domain.RefreshToken, time.Time) (uuid.UUID, error) {
	return uuid.Nil, domain.ErrInvalidToken
}

func (f *fakeStore) RevokeFamily(context.Context, []byte) error { return nil }

func newTestService(t *testing.T) (*Service, *fakeStore, *jwtx.HS256) {
	t.Helper()

	signer, err := jwtx.NewHS256(strings.Repeat("k", 32), "test")
	if err != nil {
		t.Fatal(err)
	}

	store := newFakeStore()
	svc, err := New(
		store,
		signer,
		Config{
			AccessTTL:  15 * time.Minute,
			RefreshTTL: time.Hour,
			BcryptCost: bcrypt.MinCost,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	return svc, store, signer
}

func TestRegisterStoresBcryptHash(t *testing.T) {
	svc, store, _ := newTestService(t)

	u, err := svc.Register(t.Context(), "  Guest@Example.COM ", "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "guest@example.com" {
		t.Fatalf("email not normalized: %q", u.Email)
	}

	stored := store.users["guest@example.com"]
	if stored.PasswordHash == "s3cret-pass" || bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("s3cret-pass")) != nil {
		t.Fatal("password must be stored as a bcrypt hash")
	}
}

func TestRegisterValidation(t *testing.T) {
	svc, _, _ := newTestService(t)

	cases := []struct {
		name, email, password string
		wantFields            []string
	}{
		{"bad email", "not-an-email", "longenough", []string{"email"}},
		{"email with display name", "Bob <bob@example.com>", "longenough", []string{"email"}},
		{"short password", "a@example.com", "short", []string{"password"}},
		{"password over 72 bytes", "a@example.com", strings.Repeat("x", 73), []string{"password"}},
		{"both invalid", "", "", []string{"email", "password"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Register(t.Context(), tc.email, tc.password)

			var ve *domain.ValidationError

			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if len(ve.Fields) != len(tc.wantFields) {
				t.Fatalf("fields = %+v, want %v", ve.Fields, tc.wantFields)
			}

			for i, f := range ve.Fields {
				if f.Field != tc.wantFields[i] {
					t.Fatalf("field[%d] = %q, want %q", i, f.Field, tc.wantFields[i])
				}
			}
		})
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	svc, _, _ := newTestService(t)

	if _, err := svc.Register(t.Context(), "a@example.com", "longenough"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Register(t.Context(), "A@example.com", "longenough"); !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}

func TestLogin(t *testing.T) {
	svc, store, signer := newTestService(t)
	u, err := svc.Register(t.Context(), "a@example.com", "longenough")
	if err != nil {
		t.Fatal(err)
	}

	tokens, err := svc.Login(t.Context(), "A@Example.com", "longenough")
	if err != nil {
		t.Fatal(err)
	}

	claims, err := signer.Verify(tokens.Access)
	if err != nil || claims.UserID != u.ID {
		t.Fatalf("access token invalid: %v, claims %+v", err, claims)
	}
	if tokens.ExpiresIn != 900 {
		t.Fatalf("expires_in = %d, want 900", tokens.ExpiresIn)
	}
	if _, ok := store.refresh[string(hashToken(tokens.Refresh))]; !ok {
		t.Fatal("refresh token hash must be saved, not the raw token")
	}

	for _, rt := range store.refresh {
		if string(rt.Hash) == tokens.Refresh {
			t.Fatal("raw refresh token must not be stored")
		}
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Register(t.Context(), "a@example.com", "longenough"); err != nil {
		t.Fatal(err)
	}

	_, wrongPass := svc.Login(t.Context(), "a@example.com", "wrong-password")
	_, unknown := svc.Login(t.Context(), "nobody@example.com", "longenough")

	if !errors.Is(wrongPass, domain.ErrInvalidCredentials) || !errors.Is(unknown, domain.ErrInvalidCredentials) {
		t.Fatalf("got %v and %v, want ErrInvalidCredentials for both", wrongPass, unknown)
	}
}

func TestRefreshEmptyToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Refresh(t.Context(), ""); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}
