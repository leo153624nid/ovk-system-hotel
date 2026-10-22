package transporthttp

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/services/audit/internal/domain"
	"ovk-system-hotel/services/audit/internal/service"
)

type entryResponse struct {
	ID         int64           `json:"id"`
	EventID    uuid.UUID       `json:"event_id"`
	Action     string          `json:"action"`
	EntityID   uuid.UUID       `json:"entity_id"`
	ActorID    uuid.UUID       `json:"actor_id"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

func toEntryResponse(e domain.Entry) entryResponse {
	return entryResponse{
		ID:         e.ID,
		EventID:    e.EventID,
		Action:     e.Action,
		EntityID:   e.EntityID,
		ActorID:    e.ActorID,
		Payload:    e.Payload,
		OccurredAt: e.OccurredAt,
	}
}

type pageResponse struct {
	Items      []entryResponse `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

func toPageResponse(p service.Page) pageResponse {
	items := make([]entryResponse, len(p.Items))

	for i, e := range p.Items {
		items[i] = toEntryResponse(e)
	}

	out := pageResponse{Items: items}

	if p.NextCursor != "" {
		out.NextCursor = &p.NextCursor
	}

	return out
}
