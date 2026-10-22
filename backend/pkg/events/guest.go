// Package events описывает контракт событий между сервисами (см. api/events/guest-event.schema.json).
package events

import (
	"time"

	"github.com/google/uuid"
)

const (
	GuestCreated = "guest.created"
	GuestUpdated = "guest.updated"
	GuestDeleted = "guest.deleted"
)

// StreamGuests — имя JetStream-стрима и шаблон subjects, в который публикуются события гостей.
const (
	StreamGuests   = "GUESTS"
	SubjectsGuests = "guest.>"
)

// GuestData — снимок записи гостя. Даты передаются в формате 2006-01-02.
type GuestData struct {
	ID        uuid.UUID `json:"id"`
	FullName  string    `json:"full_name"`
	Phone     string    `json:"phone"`
	Room      string    `json:"room"`
	CheckIn   string    `json:"check_in"`
	CheckOut  string    `json:"check_out"`
	Note      string    `json:"note"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GuestEvent — конверт события. Type совпадает с NATS subject.
type GuestEvent struct {
	EventID    uuid.UUID `json:"event_id"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	ActorID    uuid.UUID `json:"actor_id"`
	EntityID   uuid.UUID `json:"entity_id"`
	Data       GuestData `json:"data"`
}
