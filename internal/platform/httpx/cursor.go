package httpx

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// EncodeCursor turns the position of the last item of a page into the
// opaque next_cursor of the pagination envelope {"items", "next_cursor"}.
func EncodeCursor(position any) (string, error) {
	b, err := json.Marshal(position)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor reads a cursor made by EncodeCursor into position. A cursor
// that does not decode answers 400 validation_failed on the field cursor:
// it comes from the client, who may have changed it. A cursor that decodes
// (JSON null, missing fields) can still hold zero values: the caller checks
// the position it gets.
func DecodeCursor(cursor string, position any) error {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err == nil {
		err = json.Unmarshal(b, position)
	}
	if err != nil {
		return apperr.InvalidFields(err, "cursor")
	}
	return nil
}
