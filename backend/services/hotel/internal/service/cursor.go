package service

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/services/hotel/internal/domain"
)

type cursorJSON struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

func encodeCursor(c domain.Cursor) string {
	raw, _ := json.Marshal(cursorJSON{
		T:  c.CreatedAt,
		ID: c.ID,
	})

	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (domain.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return domain.Cursor{}, domain.ErrInvalidCursor
	}

	var c cursorJSON
	if err := json.Unmarshal(raw, &c); err != nil || c.ID == uuid.Nil || c.T.IsZero() {
		return domain.Cursor{}, domain.ErrInvalidCursor
	}

	return domain.Cursor{
		CreatedAt: c.T,
		ID:        c.ID,
	}, nil
}
