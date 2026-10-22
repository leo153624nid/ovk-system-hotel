package transporthttp

import (
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/services/hotel/internal/domain"
	"ovk-system-hotel/services/hotel/internal/service"
)

const dateLayout = "2006-01-02"

// guestRequest — тело запросов POST и PUT. Проверка значений выполняется в сервисе.
type guestRequest struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Room     string `json:"room"`
	CheckIn  string `json:"check_in"`
	CheckOut string `json:"check_out"`
	Note     string `json:"note"`
}

func (g guestRequest) toInput() service.GuestInput {
	return service.GuestInput{
		FullName: g.FullName,
		Phone:    g.Phone,
		Room:     g.Room,
		CheckIn:  g.CheckIn,
		CheckOut: g.CheckOut,
		Note:     g.Note,
	}
}

type guestResponse struct {
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

func toGuestResponse(g domain.Guest) guestResponse {
	return guestResponse{
		ID:        g.ID,
		FullName:  g.FullName,
		Phone:     g.Phone,
		Room:      g.Room,
		CheckIn:   g.CheckIn.Format(dateLayout),
		CheckOut:  g.CheckOut.Format(dateLayout),
		Note:      g.Note,
		CreatedBy: g.CreatedBy,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}
}

type pageResponse struct {
	Items      []guestResponse `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

func toPageResponse(p service.Page) pageResponse {
	items := make([]guestResponse, len(p.Items))
	for i, g := range p.Items {
		items[i] = toGuestResponse(g)
	}

	out := pageResponse{Items: items}
	if p.NextCursor != "" {
		out.NextCursor = &p.NextCursor
	}

	return out
}
