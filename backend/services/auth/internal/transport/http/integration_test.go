package transporthttp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"golang.org/x/crypto/bcrypt"

	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/auth/internal/repository"
	"ovk-system-hotel/services/auth/internal/service"
	transporthttp "ovk-system-hotel/services/auth/internal/transport/http"
	"ovk-system-hotel/services/auth/migrations"
)

type env struct {
	srv    *httptest.Server
	pool   *pgxpool.Pool
	signer *jwtx.HS256
}

// setup поднимает PostgreSQL в контейнере, применяет миграции и запускает полный стек auth.
func setup(t *testing.T) *env {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test needs Docker")
	}
	ctx := t.Context()

	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("auth_db"), postgres.WithUsername("auth"), postgres.WithPassword("auth"),
		postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.WithoutCancel(ctx)) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	signer, err := jwtx.NewHS256(strings.Repeat("s", 32), "test")
	if err != nil {
		t.Fatal(err)
	}

	repo := repository.New(pool)
	svc, err := service.New(
		repo,
		signer,
		service.Config{
			AccessTTL:  15 * time.Minute,
			RefreshTTL: time.Hour,
			BcryptCost: bcrypt.MinCost,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(transporthttp.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		svc,
		repo,
		false,
	))
	t.Cleanup(srv.Close)

	return &env{
		srv:    srv,
		pool:   pool,
		signer: signer,
	}
}

// result — ответ, уже прочитанный и закрытый, чтобы тестам не думать про Body.Close.
type result struct {
	status  int
	cookies []*http.Cookie
	body    []byte
}

func (e *env) do(t *testing.T, path string, body any, refresh string) result {
	t.Helper()

	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if refresh != "" {
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refresh})
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return result{status: resp.StatusCode, cookies: resp.Cookies(), body: raw}
}

func refreshCookie(resp result) string {
	for _, c := range resp.cookies {
		if c.Name == "refresh_token" {
			return c.Value
		}
	}

	return ""
}

func wantStatus(t *testing.T, resp result, want int) {
	t.Helper()
	if resp.status != want {
		t.Fatalf("status = %d, want %d, body: %s", resp.status, want, resp.body)
	}
}

func TestAuthFlow(t *testing.T) {
	e := setup(t)
	creds := map[string]string{"email": "Guest@Example.com", "password": "s3cret-pass"}

	t.Run("register", func(t *testing.T) {
		wantStatus(t, e.do(t, "/api/auth/register", creds, ""), http.StatusCreated)
		wantStatus(t, e.do(t, "/api/auth/register", map[string]string{"email": "guest@example.com", "password": "other-pass1"}, ""), http.StatusConflict)

		resp := e.do(t, "/api/auth/register", map[string]string{"email": "bad", "password": "x"}, "")
		wantStatus(t, resp, http.StatusUnprocessableEntity)
		var p struct {
			Errors []struct{ Field string } `json:"errors"`
		}
		if err := json.Unmarshal(resp.body, &p); err != nil || len(p.Errors) != 2 {
			t.Fatalf("expected 2 field errors, got %+v (%v)", p, err)
		}

		wantStatus(t, e.do(t, "/api/auth/register", `{"email":"a@b.co","password":"longenough","admin":true}`, ""), http.StatusBadRequest)
	})

	t.Run("password is stored as bcrypt hash", func(t *testing.T) {
		var hash string
		if err := e.pool.QueryRow(t.Context(), `SELECT password_hash FROM users WHERE email = 'guest@example.com'`).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if hash == "s3cret-pass" || !strings.HasPrefix(hash, "$2") {
			t.Fatalf("password_hash = %q, want bcrypt hash", hash)
		}
	})

	t.Run("login", func(t *testing.T) {
		wantStatus(t, e.do(t, "/api/auth/login", map[string]string{"email": "guest@example.com", "password": "wrong-pass"}, ""), http.StatusUnauthorized)
		wantStatus(t, e.do(t, "/api/auth/login", map[string]string{"email": "nobody@example.com", "password": "s3cret-pass"}, ""), http.StatusUnauthorized)
	})

	var first string
	t.Run("login issues tokens", func(t *testing.T) {
		resp := e.do(t, "/api/auth/login", creds, "")
		wantStatus(t, resp, http.StatusOK)
		var body struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			ExpiresIn   int    `json:"expires_in"`
		}
		if err := json.Unmarshal(resp.body, &body); err != nil {
			t.Fatal(err)
		}
		if _, err := e.signer.Verify(body.AccessToken); err != nil || body.TokenType != "Bearer" || body.ExpiresIn != 900 {
			t.Fatalf("bad token response: %+v, verify err %v", body, err)
		}
		c := resp.cookies[0]
		if !c.HttpOnly || c.Path != "/api/auth" || c.SameSite != http.SameSiteStrictMode {
			t.Fatalf("refresh cookie flags wrong: %+v", c)
		}
		first = c.Value
	})

	var second string
	t.Run("refresh rotates the token", func(t *testing.T) {
		resp := e.do(t, "/api/auth/refresh", nil, first)
		wantStatus(t, resp, http.StatusOK)
		second = refreshCookie(resp)
		if second == "" || second == first {
			t.Fatalf("refresh token must be rotated, got %q", second)
		}
		wantStatus(t, e.do(t, "/api/auth/refresh", nil, ""), http.StatusUnauthorized)
		wantStatus(t, e.do(t, "/api/auth/refresh", nil, "garbage"), http.StatusUnauthorized)
	})

	t.Run("reuse of a rotated token revokes the whole family", func(t *testing.T) {
		wantStatus(t, e.do(t, "/api/auth/refresh", nil, first), http.StatusUnauthorized)
		wantStatus(t, e.do(t, "/api/auth/refresh", nil, second), http.StatusUnauthorized)
	})

	t.Run("logout revokes the session", func(t *testing.T) {
		resp := e.do(t, "/api/auth/login", creds, "")
		wantStatus(t, resp, http.StatusOK)
		tok := refreshCookie(resp)

		wantStatus(t, e.do(t, "/api/auth/logout", nil, tok), http.StatusNoContent)
		wantStatus(t, e.do(t, "/api/auth/refresh", nil, tok), http.StatusUnauthorized)
		wantStatus(t, e.do(t, "/api/auth/logout", nil, ""), http.StatusNoContent)
	})
}
