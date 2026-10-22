package natsx

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"ovk-system-hotel/pkg/events"
)

const guestsStreamMaxAge = 7 * 24 * time.Hour

// GuestsStreamConfig — единая конфигурация стрима событий гостей. Её используют и издатель, и потребитель,
// чтобы CreateOrUpdateStream с обеих сторон не менял настройки друг друга.
func GuestsStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:     events.StreamGuests,
		Subjects: []string{events.SubjectsGuests},
		MaxAge:   guestsStreamMaxAge,
	}
}
