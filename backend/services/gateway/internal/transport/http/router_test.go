package transporthttp_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/gateway/internal/proxy"
	"ovk-system-hotel/services/gateway/internal/ratelimit"
	transporthttp "ovk-system-hotel/services/gateway/internal/transport/http"
)

// upstream — фейковый внутренний сервис, запоминающий последний запрос.
type upstream struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls int
	last  *http.Request
	body  string
}

func newUpstream(t *testing.T, name string) *upstream {
	t.Helper()

	u := &upstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.calls++
		u.last = r
		u.body = string(raw)
		u.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "from-" + name, Path: "/api/auth", HttpOnly: true})
		w.Header().Set("X-Upstream", name)
		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.calls
}

type env struct {
	srv                *httptest.Server
	auth, hotel, audit *upstream
	signer             *jwtx.HS256
	userID             uuid.UUID
	token              string
}

type options struct {
	general, strict *ratelimit.Limiter
	origins         []string
	trusted         []string
}

func setup(t *testing.T, opt options) *env {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer, err := jwtx.NewHS256(strings.Repeat("g", 32), "test")
	if err != nil {
		t.Fatal(err)
	}

	if opt.general == nil {
		opt.general = ratelimit.New(rate.Limit(1000), 1000, time.Minute)
	}
	if opt.strict == nil {
		opt.strict = ratelimit.New(rate.Limit(1000), 1000, time.Minute)
	}

	clientIP, err := transporthttp.NewClientIP(opt.trusted)
	if err != nil {
		t.Fatal(err)
	}

	e := &env{
		auth: newUpstream(t, "auth"), hotel: newUpstream(t, "hotel"), audit: newUpstream(t, "audit"),
		signer: signer, userID: uuid.New(),
	}
	e.token, _ = signer.Sign(e.userID, time.Hour)

	mk := func(u *upstream) http.Handler {
		target, _ := url.Parse(u.srv.URL)
		return proxy.New(log, target, clientIP)
	}
	e.srv = httptest.NewServer(transporthttp.NewRouter(transporthttp.Deps{
		Log: log, Verifier: signer, ClientIP: clientIP,
		Auth: mk(e.auth), Hotel: mk(e.hotel), Audit: mk(e.audit),
		General: opt.general, AuthStrict: opt.strict, CORSOrigins: opt.origins,
	}))

	t.Cleanup(e.srv.Close)
	return e
}

// result — ответ, уже прочитанный и закрытый.
type result struct {
	status  int
	header  http.Header
	cookies []*http.Cookie
	body    []byte
}

func (e *env) do(t *testing.T, method, path string, body string, hdr map[string]string) result {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range hdr {
		req.Header.Set(k, v)
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

	return result{
		status:  resp.StatusCode,
		header:  resp.Header,
		cookies: resp.Cookies(),
		body:    raw,
	}
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	e := setup(t, options{})
	expired, _ := e.signer.Sign(e.userID, -time.Minute)

	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/guests"},
		{http.MethodPost, "/api/guests"},
		{http.MethodGet, "/api/guests/" + uuid.NewString()},
		{http.MethodDelete, "/api/guests/" + uuid.NewString()},
		{http.MethodGet, "/api/audit"},
	}
	tokens := map[string]map[string]string{
		"no token":      nil,
		"garbage token": bearer("abc.def.ghi"),
		"expired token": bearer(expired),
	}

	for _, p := range paths {
		for name, hdr := range tokens {
			t.Run(p.method+" "+p.path+" "+name, func(t *testing.T) {
				resp := e.do(t, p.method, p.path, "", hdr)

				if resp.status != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", resp.status)
				}
				if ct := resp.header.Get("Content-Type"); ct != "application/problem+json" {
					t.Fatalf("content-type = %q", ct)
				}
			})
		}
	}

	if n := e.hotel.callCount() + e.audit.callCount(); n != 0 {
		t.Fatalf("services were called %d times without a valid token", n)
	}
}

func TestAuthRoutesArePublicAndPassThrough(t *testing.T) {
	e := setup(t, options{})

	resp := e.do(t, http.MethodPost, "/api/auth/login", `{"email":"a@b.co"}`, map[string]string{"Content-Type": "application/json"})
	if resp.status != http.StatusOK || resp.header.Get("X-Upstream") != "auth" {
		t.Fatalf("status = %d, upstream = %q", resp.status, resp.header.Get("X-Upstream"))
	}
	if e.auth.last.URL.Path != "/api/auth/login" || e.auth.body != `{"email":"a@b.co"}` {
		t.Fatalf("path/body not preserved: %q %q", e.auth.last.URL.Path, e.auth.body)
	}

	var cookie *http.Cookie
	for _, c := range resp.cookies {
		if c.Name == "refresh_token" {
			cookie = c
		}
	}

	if cookie == nil || cookie.Value != "from-auth" || !cookie.HttpOnly || cookie.Path != "/api/auth" {
		t.Fatalf("Set-Cookie from auth must reach the client intact: %+v", cookie)
	}

	for _, p := range []string{"/api/auth/register", "/api/auth/refresh", "/api/auth/logout"} {
		if r := e.do(t, http.MethodPost, p, "", nil); r.status != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 without a token", p, r.status)
		}
	}
}

func TestForwardsIdentityAndContext(t *testing.T) {
	e := setup(t, options{})

	resp := e.do(t, http.MethodGet, "/api/guests?q=a%20b&limit=2", "", map[string]string{
		"Authorization": "Bearer " + e.token,
		"X-User-ID":     uuid.NewString(), // попытка подмены
		"X-Request-ID":  "req-123",
	})
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}

	got := e.hotel.last

	if got.Header.Get("X-User-ID") != e.userID.String() {
		t.Fatalf("X-User-ID = %q, want id from the token %s", got.Header.Get("X-User-ID"), e.userID)
	}
	if got.Header.Get("Authorization") != "Bearer "+e.token {
		t.Fatal("Authorization must be forwarded so services can verify the token themselves")
	}
	if got.Header.Get("X-Request-ID") != "req-123" || resp.header.Get("X-Request-ID") != "req-123" {
		t.Fatalf("request id lost: upstream %q, response %q", got.Header.Get("X-Request-ID"), resp.header.Get("X-Request-ID"))
	}
	if got.URL.Path != "/api/guests" || got.URL.RawQuery != "q=a%20b&limit=2" {
		t.Fatalf("path/query not preserved: %s?%s", got.URL.Path, got.URL.RawQuery)
	}
	if !strings.HasPrefix(got.Header.Get("X-Forwarded-For"), "127.0.0.1") {
		t.Fatalf("X-Forwarded-For = %q", got.Header.Get("X-Forwarded-For"))
	}
}

func TestSpoofedUserIDIsStrippedOnPublicRoutes(t *testing.T) {
	e := setup(t, options{})
	e.do(t, http.MethodPost, "/api/auth/login", "{}", map[string]string{"X-User-ID": uuid.NewString()})

	if v := e.auth.last.Header.Get("X-User-ID"); v != "" {
		t.Fatalf("client-supplied X-User-ID leaked to auth: %q", v)
	}
}

func TestRequestIDGeneratedWhenAbsent(t *testing.T) {
	e := setup(t, options{})
	resp := e.do(t, http.MethodGet, "/api/guests", "", bearer(e.token))
	id := resp.header.Get("X-Request-ID")

	if id == "" || e.hotel.last.Header.Get("X-Request-ID") != id {
		t.Fatalf("response id %q must equal upstream id %q", id, e.hotel.last.Header.Get("X-Request-ID"))
	}
}

func TestUpstreamDownReturns502WithoutLeaking(t *testing.T) {
	e := setup(t, options{})
	e.hotel.srv.Close()

	resp := e.do(t, http.MethodGet, "/api/guests", "", bearer(e.token))
	if resp.status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.status)
	}

	raw := resp.body
	var p struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}

	if err := json.Unmarshal(raw, &p); err != nil || p.Type != "bad_gateway" || p.RequestID == "" {
		t.Fatalf("unexpected body %s (%v)", raw, err)
	}
	if strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), "connection refused") {
		t.Fatalf("internal details leaked: %s", raw)
	}
}

func TestHealthAndNotFound(t *testing.T) {
	e := setup(t, options{})

	for _, p := range []string{"/healthz", "/readyz"} {
		if r := e.do(t, http.MethodGet, p, "", nil); r.status != http.StatusOK {
			t.Fatalf("%s = %d", p, r.status)
		}
	}

	r := e.do(t, http.MethodGet, "/api/unknown", "", bearer(e.token))

	if r.status != http.StatusNotFound || r.header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status = %d, content-type = %q", r.status, r.header.Get("Content-Type"))
	}
}

func TestGeneralRateLimit(t *testing.T) {
	e := setup(t, options{general: ratelimit.New(rate.Every(time.Hour), 3, time.Minute)})

	for i := range 3 {
		if r := e.do(t, http.MethodGet, "/api/guests", "", bearer(e.token)); r.status != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i+1, r.status)
		}
	}

	r := e.do(t, http.MethodGet, "/api/guests", "", bearer(e.token))

	if r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
		t.Fatalf("status = %d, Retry-After = %q; want 429 with Retry-After", r.status, r.header.Get("Retry-After"))
	}
	if r := e.do(t, http.MethodGet, "/healthz", "", nil); r.status != http.StatusOK {
		t.Fatal("health checks must not be rate limited")
	}
}

func TestStrictLimitOnLoginAndRegisterOnly(t *testing.T) {
	e := setup(t, options{strict: ratelimit.New(rate.Every(time.Hour), 2, time.Minute)})

	for i := range 2 {
		if r := e.do(t, http.MethodPost, "/api/auth/login", "{}", nil); r.status != http.StatusOK {
			t.Fatalf("login %d = %d", i+1, r.status)
		}
	}

	if r := e.do(t, http.MethodPost, "/api/auth/login", "{}", nil); r.status != http.StatusTooManyRequests {
		t.Fatalf("3rd login = %d, want 429", r.status)
	}
	if r := e.do(t, http.MethodPost, "/api/auth/register", "{}", nil); r.status != http.StatusTooManyRequests {
		t.Fatalf("register shares the strict bucket, got %d, want 429", r.status)
	}
	// refresh и остальное API под строгий лимит не попадают.
	if r := e.do(t, http.MethodPost, "/api/auth/refresh", "", nil); r.status != http.StatusOK {
		t.Fatalf("refresh = %d, want 200", r.status)
	}
	if r := e.do(t, http.MethodGet, "/api/guests", "", bearer(e.token)); r.status != http.StatusOK {
		t.Fatalf("guests = %d, want 200", r.status)
	}
}

func TestRateLimitIsPerClientIP(t *testing.T) {
	e := setup(t, options{
		general: ratelimit.New(rate.Every(time.Hour), 1, time.Minute),
		trusted: []string{"127.0.0.0/8"},
	})

	call := func(ip string) int {
		return e.do(t, http.MethodGet, "/api/guests", "", map[string]string{"Authorization": "Bearer " + e.token, "X-Real-IP": ip}).status
	}

	if call("203.0.113.1") != http.StatusOK || call("203.0.113.1") != http.StatusTooManyRequests {
		t.Fatal("the same client must be limited")
	}
	if call("203.0.113.2") != http.StatusOK {
		t.Fatal("another client must have its own quota")
	}
}

func TestBodyTooLarge(t *testing.T) {
	e := setup(t, options{})

	r := e.do(t, http.MethodPost, "/api/guests", strings.Repeat("x", 1<<20+1), bearer(e.token))

	if r.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", r.status)
	}
	if e.hotel.callCount() != 0 {
		t.Fatal("oversized request must not reach the service")
	}
}

func TestCORS(t *testing.T) {
	e := setup(t, options{origins: []string{"http://localhost:5173"}})

	t.Run("preflight from allowed origin is answered by the gateway", func(t *testing.T) {
		r := e.do(t, http.MethodOptions, "/api/guests", "", map[string]string{
			"Origin": "http://localhost:5173", "Access-Control-Request-Method": "POST",
		})

		if r.status != http.StatusNoContent ||
			r.header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" ||
			r.header.Get("Access-Control-Allow-Credentials") != "true" ||
			!strings.Contains(r.header.Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Fatalf("status = %d, headers = %v", r.status, r.header)
		}
		if e.hotel.callCount() != 0 {
			t.Fatal("preflight must not reach the service")
		}
	})

	t.Run("actual request from allowed origin", func(t *testing.T) {
		hdr := bearer(e.token)
		hdr["Origin"] = "http://localhost:5173"

		r := e.do(t, http.MethodGet, "/api/guests", "", hdr)

		if r.header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || r.header.Get("Vary") != "Origin" {
			t.Fatalf("headers = %v", r.header)
		}
	})

	t.Run("foreign origin gets no CORS headers", func(t *testing.T) {
		r := e.do(t, http.MethodOptions, "/api/guests", "", map[string]string{
			"Origin": "http://evil.example", "Access-Control-Request-Method": "POST",
		})

		if r.header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("ACAO = %q for a foreign origin", r.header.Get("Access-Control-Allow-Origin"))
		}
	})
}

func TestClientIP(t *testing.T) {
	trusted, err := transporthttp.NewClientIP([]string{"10.0.0.0/8", " fd00::/8 "})
	if err != nil {
		t.Fatal(err)
	}
	none, _ := transporthttp.NewClientIP(nil)

	cases := []struct {
		name     string
		resolver func(*http.Request) string
		remote   string
		realIP   string
		want     string
	}{
		{"no proxies configured ignores header", none, "203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},
		{"untrusted peer cannot spoof", trusted, "203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},
		{"trusted proxy header is used", trusted, "10.1.2.3:5555", "198.51.100.7", "198.51.100.7"},
		{"trusted proxy without header", trusted, "10.1.2.3:5555", "", "10.1.2.3"},
		{"trusted proxy with garbage header", trusted, "10.1.2.3:5555", "not-an-ip", "10.1.2.3"},
		{"ipv6 trusted proxy", trusted, "[fd00::1]:80", "2001:db8::1", "2001:db8::1"},
		{"ipv6 client", none, "[2001:db8::5]:80", "", "2001:db8::5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote

			if tc.realIP != "" {
				r.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := tc.resolver(r); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	if _, err := transporthttp.NewClientIP([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}
