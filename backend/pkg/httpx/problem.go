package httpx

import (
	"encoding/json"
	"net/http"
)

// FieldError описывает ошибку валидации конкретного поля.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Problem — тело ошибки по RFC 7807.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail,omitempty"`
	RequestID string       `json:"request_id"`
	Errors    []FieldError `json:"errors,omitempty"`
}

// WriteJSON пишет v как JSON с указанным статусом.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

// WriteProblem пишет ошибку в формате application/problem+json. code — стабильный машинный код ошибки.
func WriteProblem(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code,
	detail string,
	errs ...FieldError,
) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(Problem{
		Type:      code,
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		RequestID: RequestIDFrom(r.Context()),
		Errors:    errs,
	})
}
