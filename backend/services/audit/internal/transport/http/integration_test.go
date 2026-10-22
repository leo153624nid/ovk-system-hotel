package transporthttp_test

import (
	"context"
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"ovk-system-hotel/pkg/dbx"
	"ovk-system-hotel/pkg/events"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/pkg/natsx"
	"ovk-system-hotel/services/audit/internal/consumer"
	"ovk-system-hotel/services/audit/internal/repository"
	"ovk-system-hotel/services/audit/internal/service"
	transporthttp "ovk-system-hotel/services/audit/internal/transport/http"
	"ovk-system-hotel/services/audit/migrations"
)

const consumerName = "audit"

type env struct {
	srv     *httptest.Server
	pool    *pgxpool.Pool
	pub     *natsx.Publisher
	natsURL string
	token   string
}

// setup поднимает PostgreSQL и NATS в контейнерах и запускает сервис целиком: потребитель событий и HTTP API.
func setup(t *testing.T) *env {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test needs Docker")
	}

	ctx := t.Context()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("audit_db"), postgres.WithUsername("audit"), postgres.WithPassword("audit"),
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

	nc, err := tcnats.Run(ctx, "nats:2-alpine")
	if err != nil {
		t.Fatalf("start nats: %v", err)
	}
	t.Cleanup(func() { _ = nc.Terminate(context.WithoutCancel(ctx)) })

	natsURL, err := nc.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pub, err := natsx.NewPublisher(natsURL, natsx.GuestsStreamConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pub.Close)

	repo := repository.New(pool)
	svc := service.New(repo)

	cctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		_ = natsx.RunConsumer(cctx, discard, natsx.ConsumerConfig{
			URL:    natsURL,
			Name:   "audit-test",
			Stream: natsx.GuestsStreamConfig(),
			Consumer: jetstream.ConsumerConfig{
				Durable:       consumerName,
				AckPolicy:     jetstream.AckExplicitPolicy,
				FilterSubject: events.SubjectsGuests,
			},
		}, consumer.Handler(svc))
	}()

	t.Cleanup(func() { cancel(); wg.Wait() })

	signer, err := jwtx.NewHS256(strings.Repeat("a", 32), "test")
	if err != nil {
		t.Fatal(err)
	}

	token, err := signer.Sign(uuid.New(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(transporthttp.NewRouter(discard, svc, repo, signer))
	t.Cleanup(srv.Close)

	return &env{
		srv:     srv,
		pool:    pool,
		pub:     pub,
		natsURL: natsURL,
		token:   token,
	}
}

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func guestEvent(typ string, entity uuid.UUID, at time.Duration) events.GuestEvent {
	return events.GuestEvent{
		EventID:    uuid.New(),
		Type:       typ,
		OccurredAt: t0.Add(at),
		ActorID:    uuid.New(),
		EntityID:   entity,
		Data: events.GuestData{
			ID:       entity,
			FullName: "Иван Петров",
			Phone:    "+79991234567",
			Room:     "204",
			CheckIn:  "2026-11-01",
			CheckOut: "2026-11-05",
		},
	}
}

func (e *env) publish(t *testing.T, ev events.GuestEvent) {
	t.Helper()

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	// Уникальный msgID, чтобы JetStream не отбросил повтор события: дедупликация должна работать на стороне журнала.
	if err := e.pub.Publish(t.Context(), ev.Type, uuid.NewString(), raw); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T) int {
	t.Helper()

	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func (e *env) waitForCount(t *testing.T, want int) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)

	for e.count(t) != want {
		if time.Now().After(deadline) {
			t.Fatalf("audit_log has %d rows, want %d", e.count(t), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type entry struct {
	ID         int64           `json:"id"`
	EventID    string          `json:"event_id"`
	Action     string          `json:"action"`
	EntityID   string          `json:"entity_id"`
	ActorID    string          `json:"actor_id"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type page struct {
	Items      []entry `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func (e *env) get(t *testing.T, path, token string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
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

	return resp.StatusCode, raw
}

func (e *env) list(t *testing.T, query string) page {
	t.Helper()

	status, raw := e.get(t, "/api/audit"+query, e.token)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body: %s", status, raw)
	}

	var p page
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}

	return p
}

func TestRequiresToken(t *testing.T) {
	e := setup(t)

	for name, token := range map[string]string{"no token": "", "garbage token": "abc.def.ghi"} {
		t.Run(name, func(t *testing.T) {
			if status, _ := e.get(t, "/api/audit", token); status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", status)
			}
		})
	}
}

func TestJournalEndToEnd(t *testing.T) {
	e := setup(t)
	a, b := uuid.New(), uuid.New()

	created := guestEvent(events.GuestCreated, a, 0)
	e.publish(t, created)
	e.publish(t, guestEvent(events.GuestUpdated, a, time.Second))
	e.publish(t, guestEvent(events.GuestDeleted, a, 2*time.Second))
	e.publish(t, guestEvent(events.GuestCreated, b, 3*time.Second))
	e.waitForCount(t, 4)

	t.Run("journal is newest first with full entries", func(t *testing.T) {
		p := e.list(t, "")
		if len(p.Items) != 4 || p.NextCursor != nil {
			t.Fatalf("want 4 items and no cursor, got %d / %v", len(p.Items), p.NextCursor)
		}

		var got []string
		for _, it := range p.Items {
			got = append(got, it.Action)
		}

		if strings.Join(got, ",") != "created,deleted,updated,created" {
			t.Fatalf("order = %v", got)
		}

		first := p.Items[0]

		if first.EntityID != b.String() || first.EventID == "" || first.ActorID == "" || !first.OccurredAt.Equal(t0.Add(3*time.Second)) {
			t.Fatalf("unexpected entry: %+v", first)
		}
		if !strings.Contains(string(first.Payload), `"room":"204"`) {
			t.Fatalf("payload must carry the guest snapshot: %s", first.Payload)
		}
	})

	t.Run("filter by entity", func(t *testing.T) {
		p := e.list(t, "?entity_id="+a.String())

		if len(p.Items) != 3 {
			t.Fatalf("want 3 entries for the guest, got %d", len(p.Items))
		}

		for _, it := range p.Items {
			if it.EntityID != a.String() {
				t.Fatalf("foreign entry in a filtered list: %+v", it)
			}
		}

		if p.Items[0].Action != "deleted" || p.Items[2].Action != "created" {
			t.Fatalf("history must be newest first: %+v", p.Items)
		}
		if empty := e.list(t, "?entity_id="+uuid.NewString()); len(empty.Items) != 0 {
			t.Fatal("unknown entity must give an empty list")
		}
	})

	t.Run("pagination", func(t *testing.T) {
		p1 := e.list(t, "?limit=3")
		if len(p1.Items) != 3 || p1.NextCursor == nil {
			t.Fatalf("page 1: %d items, cursor %v", len(p1.Items), p1.NextCursor)
		}

		p2 := e.list(t, "?limit=3&cursor="+url.QueryEscape(*p1.NextCursor))
		if len(p2.Items) != 1 || p2.NextCursor != nil {
			t.Fatalf("page 2: %d items, cursor %v", len(p2.Items), p2.NextCursor)
		}
		if p2.Items[0].ID == p1.Items[0].ID || p2.Items[0].Action != "created" || p2.Items[0].EntityID != a.String() {
			t.Fatalf("page 2 must continue after page 1: %+v", p2.Items[0])
		}
	})

	t.Run("bad parameters", func(t *testing.T) {
		for _, q := range []string{"?limit=0", "?limit=101", "?limit=x", "?entity_id=nope", "?cursor=garbage"} {
			if status, _ := e.get(t, "/api/audit"+q, e.token); status != http.StatusBadRequest {
				t.Fatalf("%s -> %d, want 400", q, status)
			}
		}
	})

	t.Run("redelivered event is stored once and a poison message does not block the queue", func(t *testing.T) {
		before := e.count(t)

		e.publish(t, created) // тот же event_id, другой msgID: дубликат на уровне журнала
		if err := e.pub.Publish(t.Context(), events.GuestCreated, uuid.NewString(), []byte("{definitely not json")); err != nil {
			t.Fatal(err)
		}

		sentinel := guestEvent(events.GuestCreated, uuid.New(), 4*time.Second)
		e.publish(t, sentinel)

		// Сообщения обрабатываются по порядку: раз пришёл sentinel, и дубликат, и битое сообщение уже обработаны.
		e.waitForCount(t, before+1)

		var n int
		if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE event_id = $1`, created.EventID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("event %s stored %d times (%v), want exactly once", created.EventID, n, err)
		}

		nc, err := nats.Connect(e.natsURL)
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()

		js, err := jetstream.New(nc)
		if err != nil {
			t.Fatal(err)
		}

		cons, err := js.Consumer(t.Context(), events.StreamGuests, consumerName)
		if err != nil {
			t.Fatal(err)
		}

		info, err := cons.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if info.NumAckPending != 0 || info.NumPending != 0 {
			t.Fatalf("consumer still holds messages (ack pending %d, pending %d): the poison message must be terminated, not retried",
				info.NumAckPending, info.NumPending)
		}
	})
}
