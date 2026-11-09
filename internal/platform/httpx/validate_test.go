package httpx

import (
	"errors"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

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
