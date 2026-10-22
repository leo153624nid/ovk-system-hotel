package consumer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ovk-system-hotel/pkg/natsx"
	"ovk-system-hotel/services/audit/internal/domain"
)

type fakeRecorder struct{ err error }

func (f fakeRecorder) Record(context.Context, string, []byte) error { return f.err }

func TestHandlerClassifiesErrors(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantErr       bool
		wantPermanent bool
	}{
		{"success is acked", nil, false, false},
		{"invalid event is terminated", fmt.Errorf("%w: malformed", domain.ErrInvalidEvent), true, true},
		{"storage failure is retried", errors.New("db down"), true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Handler(fakeRecorder{err: tc.err})(t.Context(), "guest.created", []byte("{}"))

			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if errors.Is(err, natsx.ErrPermanent) != tc.wantPermanent {
				t.Fatalf("permanent = %v, want %v (err: %v)", errors.Is(err, natsx.ErrPermanent), tc.wantPermanent, err)
			}
		})
	}
}
