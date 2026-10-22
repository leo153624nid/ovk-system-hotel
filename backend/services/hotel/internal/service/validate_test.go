package service

import (
	"errors"
	"strings"
	"testing"

	"ovk-system-hotel/services/hotel/internal/domain"
)

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"+79991234567", "+79991234567", true},
		{"+7 (999) 123-45-67", "+79991234567", true},
		{"8 999 123 45 67", "+79991234567", true},
		{"79991234567", "+79991234567", true},
		{"+1 202 555 0143", "+12025550143", true},
		{"  +44.20.7946.0958 ", "+442079460958", true},
		{"", "", false},
		{"89991234", "", false},          // без «+» и не 11 цифр
		{"+0123456789", "", false},       // код страны не начинается с 0
		{"+7999", "", false},             // слишком короткий
		{"+1234567890123456", "", false}, // длиннее 15 цифр
		{"+7999abc4567", "", false},
		{"7+9991234567", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := normalizePhone(tc.in)

			if ok != tc.ok || got != tc.want {
				t.Fatalf("normalizePhone(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func validInput() GuestInput {
	return GuestInput{
		FullName: "Иван Петров",
		Phone:    "+7 999 123-45-67",
		Room:     "204",
		CheckIn:  "2026-11-01",
		CheckOut: "2026-11-05",
		Note:     "Поздний заезд",
	}
}

func TestValidateNormalizesValidInput(t *testing.T) {
	in := validInput()
	in.FullName = "  Иван Петров  "

	got, err := validate(in)
	if err != nil {
		t.Fatal(err)
	}

	if got.fullName != "Иван Петров" || got.phone != "+79991234567" {
		t.Fatalf("not normalized: %+v", got)
	}
}

func TestValidateAllowsSameDayStay(t *testing.T) {
	in := validInput()
	in.CheckOut = in.CheckIn

	if _, err := validate(in); err != nil {
		t.Fatalf("check_out == check_in must be allowed: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*GuestInput)
		field  string
	}{
		{"empty name", func(g *GuestInput) { g.FullName = "   " }, "full_name"},
		{"long name", func(g *GuestInput) { g.FullName = strings.Repeat("я", 201) }, "full_name"},
		{"bad phone", func(g *GuestInput) { g.Phone = "123" }, "phone"},
		{"empty room", func(g *GuestInput) { g.Room = "" }, "room"},
		{"long room", func(g *GuestInput) { g.Room = strings.Repeat("1", 21) }, "room"},
		{"bad check_in", func(g *GuestInput) { g.CheckIn = "01.11.2026" }, "check_in"},
		{"missing check_out", func(g *GuestInput) { g.CheckOut = "" }, "check_out"},
		{"check_out before check_in", func(g *GuestInput) { g.CheckOut = "2026-10-31" }, "check_out"},
		{"long note", func(g *GuestInput) { g.Note = strings.Repeat("я", 2001) }, "note"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			_, err := validate(in)

			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if len(ve.Fields) != 1 || ve.Fields[0].Field != tc.field {
				t.Fatalf("fields = %+v, want exactly [%s]", ve.Fields, tc.field)
			}
		})
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	_, err := validate(GuestInput{})

	var ve *domain.ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 5 {
		t.Fatalf("want 5 field errors for empty input, got %v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	want := domain.Cursor{CreatedAt: mustTime(t), ID: mustUUID(t)}
	got, err := decodeCursor(encodeCursor(want))

	if err != nil {
		t.Fatal(err)
	}

	if !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, s := range []string{"!!!", "e30", "bm90LWpzb24"} {
		if _, err := decodeCursor(s); !errors.Is(err, domain.ErrInvalidCursor) {
			t.Fatalf("decodeCursor(%q) err = %v, want ErrInvalidCursor", s, err)
		}
	}
}
