package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxBodyBytes = 1 << 20

// DecodeJSON читает тело запроса в dst. Неизвестные поля, лишние данные после значения
// и тело больше 1 МБ считаются ошибкой.
func DecodeJSON(
	w http.ResponseWriter,
	r *http.Request,
	dst any,
) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errors.New("request body too large")
		}

		return fmt.Errorf("invalid JSON: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON: unexpected data after top-level value")
	}

	return nil
}
