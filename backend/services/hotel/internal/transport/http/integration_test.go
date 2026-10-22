package transporthttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"ovk-system-hotel/pkg/dbx"
	"ovk-system-hotel/pkg/events"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/hotel/internal/domain"
	"ovk-system-hotel/services/hotel/internal/repository"
	"ovk-system-hotel/services/hotel/internal/service"
	transporthttp "ovk-system-hotel/services/hotel/internal/transport/http"
	"ovk-system-hotel/services/hotel/migrations"
)

type env struct {
	srv   *httptest.Server
	pool  *pgxpool.Pool
	repo  *repository.Repository
	token string
	actor uuid.UUID
}

// setup поднимает PostgreSQL в контейнере, применяет миграции и запускает полный стек hotel.
func setup(t *testing.T) *env {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test needs Docker")
	}

	ctx := t.Context()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("hotel_db"), postgres.WithUsername("hotel"), postgres.WithPassword("hotel"),
		postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.WithoutCancel(ctx)) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := dbx.Migrate(ctx, discard, dsn, migrations.FS); err != nil {
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

	actor := uuid.New()
	token, err := signer.Sign(actor, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	repo := repository.New(pool)
	srv := httptest.NewServer(
		transporthttp.NewRouter(
			discard, service.New(repo),
			repo,
			signer,
		),
	)
	t.Cleanup(srv.Close)

	return &env{
		srv:   srv,
		pool:  pool,
		repo:  repo,
		token: token,
		actor: actor,
	}
}

type result struct {
	status int
	header http.Header
	body   []byte
}

func (r result) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func (e *env) request(t *testing.T, method, path, token string, body any) result {
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

	req, err := http.NewRequestWithContext(t.Context(), method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
		status: resp.StatusCode,
		header: resp.Header,
		body:   raw,
	}
}

// do выполняет запрос от имени авторизованного пользователя.
func (e *env) do(t *testing.T, method, path string, body any) result {
	t.Helper()
	return e.request(t, method, path, e.token, body)
}

func wantStatus(t *testing.T, r result, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d, body: %s", r.status, want, r.body)
	}
}

type guest struct {
	ID        string `json:"id"`
	FullName  string `json:"full_name"`
	Phone     string `json:"phone"`
	Room      string `json:"room"`
	CheckIn   string `json:"check_in"`
	CheckOut  string `json:"check_out"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
}

type page struct {
	Items      []guest `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func input(name, phone, room string) map[string]string {
	return map[string]string{
		"full_name": name, "phone": phone, "room": room,
		"check_in": "2026-11-01", "check_out": "2026-11-05", "note": "",
	}
}

func (e *env) create(t *testing.T, in map[string]string) guest {
	t.Helper()

	r := e.do(t, http.MethodPost, "/api/guests", in)
	wantStatus(t, r, http.StatusCreated)

	var g guest
	r.decode(t, &g)

	return g
}

func (e *env) list(t *testing.T, query string) page {
	t.Helper()

	r := e.do(t, http.MethodGet, "/api/guests"+query, nil)
	wantStatus(t, r, http.StatusOK)

	var p page
	r.decode(t, &p)

	return p
}

func TestRequiresToken(t *testing.T) {
	e := setup(t)
	id := uuid.NewString()

	endpoints := []struct{ method, path string }{
		{http.MethodGet, "/api/guests"},
		{http.MethodPost, "/api/guests"},
		{http.MethodGet, "/api/guests/" + id},
		{http.MethodPut, "/api/guests/" + id},
		{http.MethodDelete, "/api/guests/" + id},
	}

	for _, ep := range endpoints {
		for name, token := range map[string]string{"no token": "", "garbage token": "abc.def.ghi"} {
			t.Run(fmt.Sprintf("%s %s %s", ep.method, ep.path, name), func(t *testing.T) {
				wantStatus(t, e.request(t, ep.method, ep.path, token, nil), http.StatusUnauthorized)
			})
		}
	}
}

func TestCRUD(t *testing.T) {
	e := setup(t)

	created := e.create(t, map[string]string{
		"full_name": "  Иван Петров ", "phone": "8 (999) 123-45-67", "room": "204",
		"check_in": "2026-11-01", "check_out": "2026-11-05", "note": "Поздний заезд",
	})
	if created.FullName != "Иван Петров" || created.Phone != "+79991234567" || created.CreatedBy != e.actor.String() {
		t.Fatalf("unexpected created guest: %+v", created)
	}

	t.Run("create sets Location", func(t *testing.T) {
		r := e.do(t, http.MethodPost, "/api/guests", input("Анна", "+79990000001", "1"))
		wantStatus(t, r, http.StatusCreated)

		var g guest
		r.decode(t, &g)

		if r.header.Get("Location") != "/api/guests/"+g.ID {
			t.Fatalf("Location = %q", r.header.Get("Location"))
		}
	})

	t.Run("get", func(t *testing.T) {
		r := e.do(t, http.MethodGet, "/api/guests/"+created.ID, nil)
		wantStatus(t, r, http.StatusOK)

		var g guest
		r.decode(t, &g)

		if g != created {
			t.Fatalf("got %+v, want %+v", g, created)
		}
	})

	t.Run("update", func(t *testing.T) {
		in := input("Иван Петров-Сидоров", "+79991234567", "305")
		in["note"] = "Переселили"
		r := e.do(t, http.MethodPut, "/api/guests/"+created.ID, in)
		wantStatus(t, r, http.StatusOK)

		var g guest
		r.decode(t, &g)

		if g.Room != "305" || g.Note != "Переселили" || g.CreatedBy != e.actor.String() {
			t.Fatalf("unexpected updated guest: %+v", g)
		}
	})

	t.Run("update and get unknown id", func(t *testing.T) {
		missing := "/api/guests/" + uuid.NewString()
		wantStatus(t, e.do(t, http.MethodGet, missing, nil), http.StatusNotFound)
		wantStatus(t, e.do(t, http.MethodPut, missing, input("Имя", "+79990000002", "1")), http.StatusNotFound)
		wantStatus(t, e.do(t, http.MethodDelete, missing, nil), http.StatusNotFound)
	})

	t.Run("malformed id", func(t *testing.T) {
		wantStatus(t, e.do(t, http.MethodGet, "/api/guests/not-a-uuid", nil), http.StatusBadRequest)
	})

	t.Run("validation errors", func(t *testing.T) {
		bad := input("", "123", "")
		bad["check_out"] = "2026-10-01"
		r := e.do(t, http.MethodPost, "/api/guests", bad)
		wantStatus(t, r, http.StatusUnprocessableEntity)

		var p struct {
			Errors []struct{ Field string } `json:"errors"`
		}

		r.decode(t, &p)

		if len(p.Errors) != 4 {
			t.Fatalf("want 4 field errors, got %+v", p.Errors)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		wantStatus(t, e.do(t, http.MethodPost, "/api/guests", "{not json"), http.StatusBadRequest)
		wantStatus(t, e.do(t, http.MethodPost, "/api/guests", `{"full_name":"x","unknown":1}`), http.StatusBadRequest)
	})

	t.Run("delete", func(t *testing.T) {
		wantStatus(t, e.do(t, http.MethodDelete, "/api/guests/"+created.ID, nil), http.StatusNoContent)
		wantStatus(t, e.do(t, http.MethodGet, "/api/guests/"+created.ID, nil), http.StatusNotFound)
	})
}

func TestSearchAndPagination(t *testing.T) {
	e := setup(t)
	e.create(t, input("Иван Петров", "+79991111111", "101"))
	e.create(t, input("Мария Иванова", "+79992222222", "202"))
	e.create(t, input("John Smith", "+12025550143", "303"))
	e.create(t, input("Ольга 100%_скидка", "+79993333333", "404"))
	e.create(t, input("Пётр Сидоров", "+79994444444", "505"))

	search := func(q string) []string {
		var names []string
		for _, g := range e.list(t, "?q="+url.QueryEscape(q)).Items {
			names = append(names, g.FullName)
		}
		return names
	}

	cases := []struct {
		q    string
		want int
	}{
		{"иван", 2}, // регистронезависимо, и «Иван», и «Иванова»
		{"ПЕТРОВ", 1},
		{"smith", 1},
		{"+7999222", 1}, // по телефону
		{"303", 1},      // по номеру комнаты
		{"%", 1},        // служебные символы LIKE ищутся как текст
		{"_", 1},
		{"100%", 1},
		{"нет такого", 0},
	}

	for _, tc := range cases {
		t.Run("q="+tc.q, func(t *testing.T) {
			if got := search(tc.q); len(got) != tc.want {
				t.Fatalf("q=%q matched %v, want %d", tc.q, got, tc.want)
			}
		})
	}

	t.Run("pagination walks all items without duplicates", func(t *testing.T) {
		seen := map[string]bool{}
		var order []string
		cursor := ""

		for i := 0; i < 10; i++ {
			q := "?limit=2"
			if cursor != "" {
				q += "&cursor=" + url.QueryEscape(cursor)
			}

			p := e.list(t, q)
			if len(p.Items) > 2 {
				t.Fatalf("page has %d items, limit is 2", len(p.Items))
			}

			for _, g := range p.Items {
				if seen[g.ID] {
					t.Fatalf("duplicate item %s across pages", g.ID)
				}
				seen[g.ID] = true
				order = append(order, g.Room)
			}

			if p.NextCursor == nil {
				break
			}
			cursor = *p.NextCursor
		}

		if len(seen) != 5 {
			t.Fatalf("walked %d items, want 5", len(seen))
		}
		if strings.Join(order, ",") != "505,404,303,202,101" {
			t.Fatalf("order = %v, want newest first", order)
		}
	})

	t.Run("search combined with cursor", func(t *testing.T) {
		p := e.list(t, "?q=79&limit=1")
		if len(p.Items) != 1 || p.NextCursor == nil {
			t.Fatalf("want 1 item and a cursor, got %+v", p)
		}

		next := e.list(t, "?q=79&limit=10&cursor="+url.QueryEscape(*p.NextCursor))
		if len(next.Items) != 3 || next.NextCursor != nil {
			t.Fatalf("want remaining 3 matches and no cursor, got %d / %v", len(next.Items), next.NextCursor)
		}
	})

	t.Run("empty result is an empty array", func(t *testing.T) {
		r := e.do(t, http.MethodGet, "/api/guests?q="+url.QueryEscape("нет такого"), nil)
		if !strings.Contains(string(r.body), `"items":[]`) || !strings.Contains(string(r.body), `"next_cursor":null`) {
			t.Fatalf("body = %s", r.body)
		}
	})

	t.Run("bad parameters", func(t *testing.T) {
		for _, q := range []string{"?limit=0", "?limit=101", "?limit=abc", "?cursor=garbage", "?q=" + strings.Repeat("x", 101)} {
			wantStatus(t, e.do(t, http.MethodGet, "/api/guests"+q, nil), http.StatusBadRequest)
		}
	})
}

// Индекс нужен для ILIKE '%q%': btree тут бесполезен. seqscan отключён, чтобы на маленькой таблице
// планировщик не выбирал полный перебор из-за стоимости, и проверялась именно применимость индекса.
func TestSearchIndexIsApplicable(t *testing.T) {
	e := setup(t)
	e.create(t, input("Иван Петров", "+79991111111", "101"))

	conn, err := e.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	if _, err := conn.Exec(t.Context(), `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}

	rows, err := conn.Query(t.Context(),
		`EXPLAIN SELECT id FROM guests WHERE `+repository.SearchExpr+` ILIKE '%иван%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		_, _ = plan.WriteString(line + "\n")
	}

	if !strings.Contains(plan.String(), "guests_search_trgm_idx") {
		t.Fatalf("search index is not used:\n%s", plan.String())
	}
}

func TestOutbox(t *testing.T) {
	e := setup(t)

	g := e.create(t, input("Иван Петров", "+79991111111", "101"))
	wantStatus(t, e.do(t, http.MethodPut, "/api/guests/"+g.ID, input("Иван Петров", "+79991111111", "202")), http.StatusOK)
	wantStatus(t, e.do(t, http.MethodDelete, "/api/guests/"+g.ID, nil), http.StatusNoContent)
	// Неудачные запросы не должны оставлять событий.
	wantStatus(t, e.do(t, http.MethodPost, "/api/guests", input("", "", "")), http.StatusUnprocessableEntity)
	wantStatus(t, e.do(t, http.MethodDelete, "/api/guests/"+uuid.NewString(), nil), http.StatusNotFound)

	rows, err := e.pool.Query(t.Context(), `SELECT subject, payload FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []events.GuestEvent
	for rows.Next() {
		var subject string
		var payload []byte

		if err := rows.Scan(&subject, &payload); err != nil {
			t.Fatal(err)
		}

		var ev events.GuestEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != subject {
			t.Fatalf("type %q != subject %q", ev.Type, subject)
		}
		got = append(got, ev)
	}

	if len(got) != 3 {
		t.Fatalf("outbox has %d events, want 3", len(got))
	}
	wantTypes := []string{events.GuestCreated, events.GuestUpdated, events.GuestDeleted}

	for i, ev := range got {
		if ev.Type != wantTypes[i] || ev.EntityID.String() != g.ID || ev.ActorID != e.actor {
			t.Fatalf("event[%d] = %+v", i, ev)
		}
	}

	if got[1].Data.Room != "202" || got[2].Data.Room != "202" {
		t.Fatalf("update/delete events must carry the guest snapshot: %+v / %+v", got[1].Data, got[2].Data)
	}
}

func TestPublishPendingMarksOnlyDelivered(t *testing.T) {
	e := setup(t)
	for i := range 3 {
		e.create(t, input(fmt.Sprintf("Гость %d", i), fmt.Sprintf("+7999000000%d", i), "1"))
	}

	var delivered []uuid.UUID
	failOnSecond := func(_ context.Context, ev domain.OutboxEvent) error {
		if len(delivered) == 1 {
			return errors.New("broker down")
		}

		delivered = append(delivered, ev.EventID)
		return nil
	}

	sent, err := e.repo.PublishPending(t.Context(), 10, failOnSecond)
	if sent != 1 || err == nil {
		t.Fatalf("sent = %d, err = %v; want 1 delivered and the publish error", sent, err)
	}
	e.wantUnpublished(t, 2)

	sent, err = e.repo.PublishPending(t.Context(), 10, func(context.Context, domain.OutboxEvent) error { return nil })
	if sent != 2 || err != nil {
		t.Fatalf("sent = %d, err = %v; want the remaining 2", sent, err)
	}
	e.wantUnpublished(t, 0)

	sent, err = e.repo.PublishPending(t.Context(), 10, func(context.Context, domain.OutboxEvent) error { return errors.New("must not be called") })
	if sent != 0 || err != nil {
		t.Fatalf("sent = %d, err = %v; want nothing to do", sent, err)
	}
}

func (e *env) wantUnpublished(t *testing.T, want int) {
	t.Helper()

	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("unpublished = %d, want %d", n, want)
	}
}
