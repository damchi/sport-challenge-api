package httpx

import (
	"errors"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// schemaError returns the error kin-openapi gives for the value at path.
func schemaError(t *testing.T, path ...string) error {
	t.Helper()
	schema := openapi3.NewStringSchema()
	value := any(42)
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == "0" {
			schema = openapi3.NewArraySchema().WithItems(schema)
			value = []any{value}
			continue
		}
		schema = openapi3.NewObjectSchema().WithProperty(path[i], schema)
		value = map[string]any{path[i]: value}
	}
	err := schema.VisitJSON(value, openapi3.MultiErrors())
	if err == nil {
		t.Fatalf("no error for %v", path)
	}
	return err
}

func TestFieldsOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{name: "top-level property", err: schemaError(t, "pseudo"), want: []string{"pseudo"}},
		{name: "nested property", err: schemaError(t, "items", "0", "distance"), want: []string{"items.0.distance"}},
		{
			name: "several, sorted and once each",
			err:  openapi3.MultiError{schemaError(t, "pseudo"), schemaError(t, "language"), schemaError(t, "pseudo")},
			want: []string{"language", "pseudo"},
		},
		{
			name: "body error",
			err:  &openapi3filter.RequestError{Reason: "doesn't match schema", Err: schemaError(t, "language")},
			want: []string{"language"},
		},
		{
			name: "parameter",
			err:  &openapi3filter.RequestError{Parameter: &openapi3.Parameter{Name: "cursor", In: "query"}, Err: errors.New("invalid")},
			want: []string{"cursor"},
		},
		{name: "root of the body (not JSON, unknown field)", err: openapi3.NewObjectSchema().VisitJSON("text"), want: nil},
		{name: "other error", err: errors.New("http: request body too large"), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldsOf(tt.err); !slices.Equal(got, tt.want) {
				t.Fatalf("fieldsOf() = %q, want %q", got, tt.want)
			}
		})
	}
}
