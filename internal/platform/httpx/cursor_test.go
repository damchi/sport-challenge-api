package httpx

import (
	"errors"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

type position struct {
	StartsAt time.Time `json:"s"`
	ID       string    `json:"i"`
}

func TestCursorRoundTrip(t *testing.T) {
	want := position{StartsAt: time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC), ID: "11111111-1111-1111-1111-111111111111"}
	cursor, err := EncodeCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	var got position
	if err := DecodeCursor(cursor, &got); err != nil {
		t.Fatal(err)
	}
	if !got.StartsAt.Equal(want.StartsAt) || got.ID != want.ID {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
}

func TestDecodeCursorRefusesTamperedValues(t *testing.T) {
	for _, cursor := range []string{"not base64!", "bm90IGpzb24", ""} {
		var p position
		err := DecodeCursor(cursor, &p)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Params[apperr.FieldsParam] != "cursor" {
			t.Errorf("DecodeCursor(%q) = %v, want validation_failed on cursor", cursor, err)
		}
	}
}
