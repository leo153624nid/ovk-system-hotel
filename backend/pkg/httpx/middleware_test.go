package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/pkg/jwtx"
)

func TestAuthenticate(t *testing.T) {
	signer, err := jwtx.NewHS256("0123456789abcdef0123456789abcdef", "iss")
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	good, _ := signer.Sign(uid, time.Minute)
	expired, _ := signer.Sign(uid, -time.Minute)

	var gotUser uuid.UUID
	h := RequestID(Authenticate(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _ = UserID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + good, http.StatusUnauthorized},
		{"empty token", "Bearer ", http.StatusUnauthorized},
		{"garbage", "Bearer abc", http.StatusUnauthorized},
		{"expired", "Bearer " + expired, http.StatusUnauthorized},
		{"valid", "Bearer " + good, http.StatusNoContent},
		{"scheme case-insensitive", "bearer " + good, http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusUnauthorized && rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
			}
		})
	}
	if gotUser != uid {
		t.Fatalf("user id in context = %s, want %s", gotUser, uid)
	}
}

func TestRequestIDGenerated(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Header().Get(RequestIDHeader) == "" {
		t.Fatal("request id header not set")
	}
}
