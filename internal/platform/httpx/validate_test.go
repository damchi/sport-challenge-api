package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// itemsContract has one operation, GET /items?limit= (1 to 10).
const itemsContract = `
openapi: 3.0.3
info: {title: test, version: "1"}
paths:
  /items:
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer, minimum: 1, maximum: 10}}
      responses:
        "204": {description: ok}
`

// TestRequestValidatorGate: an invalid request answers its validation error,
// with its fields, unless the gate refuses it first; then nothing about what
// was invalid is sent back. A valid request is never stopped by the gate.
func TestRequestValidatorGate(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(itemsContract))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		gate       Gate
		target     string
		wantStatus int
		wantCode   apperr.Code
		wantFields string
	}{
		{name: "invalid, no gate", target: "/items?limit=0", wantStatus: http.StatusBadRequest, wantCode: apperr.CodeValidationFailed, wantFields: "limit"},
		{name: "invalid, gate lets through", gate: passingGate, target: "/items?limit=0", wantStatus: http.StatusBadRequest, wantCode: apperr.CodeValidationFailed, wantFields: "limit"},
		{name: "invalid, gate refuses", gate: hidingGate, target: "/items?limit=0", wantStatus: http.StatusNotFound, wantCode: apperr.CodeNotFound},
		{name: "valid, gate refuses", gate: hidingGate, target: "/items?limit=5", wantStatus: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validate, err := RequestValidator(doc, discard(), tt.gate)
			if err != nil {
				t.Fatal(err)
			}
			r := gin.New()
			r.GET("/items", validate, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			rec := serve(r, httptest.NewRequest(http.MethodGet, tt.target, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body, tt.wantStatus)
			}
			if tt.wantCode == "" {
				return
			}
			got := decodeError(t, rec)
			if got.Code != tt.wantCode || got.Params["fields"] != tt.wantFields {
				t.Fatalf("got %s fields %q, want %s fields %q", got.Code, got.Params["fields"], tt.wantCode, tt.wantFields)
			}
			if tt.wantFields == "" && strings.Contains(rec.Body.String(), "limit") {
				t.Fatalf("response names the invalid field: %s", rec.Body)
			}
		})
	}
}

// schemaError returns the schema of the value at path and the error
// kin-openapi gives for it. A "*" segment is a free-form key of a map
// (additionalProperties), "0" an array index.
func schemaError(t *testing.T, path ...string) (*openapi3.Schema, error) {
	t.Helper()
	schema := openapi3.NewStringSchema()
	value := any(42)
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i] {
		case "0":
			schema = openapi3.NewArraySchema().WithItems(schema)
			value = []any{value}
		case "*":
			schema = openapi3.NewObjectSchema().WithAdditionalProperties(schema)
			value = map[string]any{"<chosen by the client>": value}
		default:
			schema = openapi3.NewObjectSchema().WithProperty(path[i], schema)
			value = map[string]any{path[i]: value}
		}
	}
	err := schema.VisitJSON(value, openapi3.MultiErrors())
	if err == nil {
		t.Fatalf("no error for %v", path)
	}
	return schema, err
}

func TestFieldsOf(t *testing.T) {
	pseudo, pseudoErr := schemaError(t, "pseudo")
	nested, nestedErr := schemaError(t, "items", "0", "distance")
	factors, factorsErr := schemaError(t, "distance_factors", "*")
	deep, deepErr := schemaError(t, "params", "*", "value")
	tests := []struct {
		name string
		err  error
		body *openapi3.Schema
		want []string
	}{
		{name: "top-level property", err: pseudoErr, body: pseudo, want: []string{"pseudo"}},
		{name: "nested property", err: nestedErr, body: nested, want: []string{"items.0.distance"}},
		{name: "free-form key: stops at the map", err: factorsErr, body: factors, want: []string{"distance_factors"}},
		{name: "free-form key in the middle", err: deepErr, body: deep, want: []string{"params"}},
		{name: "no body schema: nothing", err: pseudoErr, body: nil, want: nil},
		{
			name: "several, sorted and once each",
			err:  openapi3.MultiError{pseudoErr, factorsErr, pseudoErr},
			body: openapi3.NewObjectSchema().WithProperty("pseudo", openapi3.NewStringSchema()).WithProperty("distance_factors", openapi3.NewObjectSchema().WithAdditionalProperties(openapi3.NewStringSchema())),
			want: []string{"distance_factors", "pseudo"},
		},
		{
			name: "parameter with several broken bounds",
			err:  openapi3.MultiError{&openapi3filter.RequestError{Parameter: &openapi3.Parameter{Name: "limit", In: "query"}, Err: openapi3.MultiError{pseudoErr}}},
			body: pseudo,
			want: []string{"limit"},
		},
		{
			name: "body error wrapped in a request error",
			err:  &openapi3filter.RequestError{Err: openapi3.MultiError{pseudoErr}},
			body: pseudo,
			want: []string{"pseudo"},
		},
		{
			name: "parameter",
			err:  &openapi3filter.RequestError{Parameter: &openapi3.Parameter{Name: "cursor", In: "query"}, Err: errors.New("invalid")},
			want: []string{"cursor"},
		},
		{name: "root of the body (not JSON, unknown field)", err: openapi3.NewObjectSchema().VisitJSON("text"), body: pseudo, want: nil},
		{name: "other error", err: errors.New("http: request body too large"), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldsOf(tt.err, tt.body); !slices.Equal(got, tt.want) {
				t.Fatalf("fieldsOf() = %q, want %q", got, tt.want)
			}
		})
	}
}
