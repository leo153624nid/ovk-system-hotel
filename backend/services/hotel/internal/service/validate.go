package service

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ovk-system-hotel/services/hotel/internal/domain"
)

const (
	dateLayout    = "2006-01-02"
	maxNameLen    = 200
	maxRoomLen    = 20
	maxNoteLen    = 2000
	minPhoneDigit = 8
	maxPhoneDigit = 15
)

// GuestInput — данные записи гостя в том виде, в каком они пришли снаружи.
type GuestInput struct {
	FullName string
	Phone    string
	Room     string
	CheckIn  string // 2006-01-02
	CheckOut string // 2006-01-02
	Note     string
}

type parsed struct {
	fullName, phone, room, note string
	checkIn, checkOut           time.Time
}

// validate проверяет все поля и собирает все ошибки сразу, чтобы форма показала их одним ответом.
func validate(in GuestInput) (parsed, error) {
	var (
		out    parsed
		fields []domain.FieldError
	)
	bad := func(field, msg string) {
		fields = append(fields, domain.FieldError{Field: field, Message: msg})
	}

	out.fullName = strings.TrimSpace(in.FullName)
	if n := utf8.RuneCountInString(out.fullName); n < 1 || n > maxNameLen {
		bad("full_name", fmt.Sprintf("must be 1-%d characters", maxNameLen))
	}

	if p, ok := normalizePhone(in.Phone); ok {
		out.phone = p
	} else {
		bad("phone", "must be a valid phone number with country code, e.g. +79991234567")
	}

	out.room = strings.TrimSpace(in.Room)
	if n := utf8.RuneCountInString(out.room); n < 1 || n > maxRoomLen {
		bad("room", fmt.Sprintf("must be 1-%d characters", maxRoomLen))
	}

	var inErr, outErr bool
	var err error
	if out.checkIn, err = time.Parse(dateLayout, in.CheckIn); err != nil {
		bad("check_in", "must be a date in YYYY-MM-DD format")
		inErr = true
	}
	if out.checkOut, err = time.Parse(dateLayout, in.CheckOut); err != nil {
		bad("check_out", "must be a date in YYYY-MM-DD format")
		outErr = true
	}
	if !inErr && !outErr && out.checkOut.Before(out.checkIn) {
		bad("check_out", "must not be earlier than check_in")
	}

	out.note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(out.note) > maxNoteLen {
		bad("note", fmt.Sprintf("must be at most %d characters", maxNoteLen))
	}

	if len(fields) > 0 {
		return parsed{}, &domain.ValidationError{Fields: fields}
	}

	return out, nil
}

// normalizePhone приводит номер к E.164. Принимает пробелы, скобки, дефисы и точки как разделители.
// Номера без «+» принимаются только российского вида: 8XXXXXXXXXX и 7XXXXXXXXXX (11 цифр).
func normalizePhone(s string) (string, bool) {
	s = strings.TrimSpace(s)
	var (
		digits strings.Builder
		plus   bool
	)

	for i, r := range s {
		switch {
		case r == '+' && i == 0:
			plus = true
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case strings.ContainsRune(" -().", r):
		default:
			return "", false
		}
	}

	d := digits.String()
	if !plus && len(d) == 11 {
		switch d[0] {
		case '8':
			d, plus = "7"+d[1:], true
		case '7':
			plus = true
		}
	}

	if !plus || len(d) < minPhoneDigit || len(d) > maxPhoneDigit || d[0] == '0' {
		return "", false
	}

	return "+" + d, true
}
