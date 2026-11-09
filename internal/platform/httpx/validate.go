package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// maxRequestBody bounds the JSON bodies read and validated.
const maxRequestBody = 64 << 10

// RequestValidator checks each request against the contract before the
// generated handler decodes it: parameters, body schema (types, patterns,
// enums, null refused where not nullable, unknown fields refused). A request
// that does not match answers 400 validation_failed with the invalid fields,
// comma-separated, in params.fields. An unknown field, a body that is not
// JSON or one over maxRequestBody answers without params: only the client's
// text names the problem, and the validation messages, which echo the
// client's values, are never sent back. Authentication is not checked here:
// Protect has already run.
func RequestValidator(doc *openapi3.T, logger *slog.Logger) (gin.HandlerFunc, error) {
	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("build contract router: %w", err)
	}
	opts := &openapi3filter.Options{
		MultiError:         true,
		KeepValidatedValue: true,
		AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
	}
	return func(c *gin.Context) {
		route, pathParams, err := router.FindRoute(c.Request)
		if err != nil {
			// Gin matched the route, so it is in the contract: a miss is ours.
			RespondError(c, logger, apperr.New(apperr.CodeInternal, fmt.Errorf("find contract route: %w", err)))
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBody)
		}
		err = openapi3filter.ValidateRequest(c.Request.Context(), &openapi3filter.RequestValidationInput{
			Request:    c.Request,
			PathParams: pathParams,
			Route:      route,
			Options:    opts,
		})
		if err != nil {
			RespondError(c, logger, apperr.InvalidFields(err, fieldsOf(err, bodySchema(route))...))
		}
	}, nil
}

// bodySchema returns the JSON body schema of the route, nil without one.
func bodySchema(route *routers.Route) *openapi3.Schema {
	if route.Operation == nil || route.Operation.RequestBody == nil || route.Operation.RequestBody.Value == nil {
		return nil
	}
	media := route.Operation.RequestBody.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		return nil
	}
	return media.Schema.Value
}

// fieldsOf lists, sorted and once each, the fields an error from
// openapi3filter.ValidateRequest is about: the parameter name, or the path of
// the body property ("pseudo", "items.0.distance"). A body path keeps only
// the property names declared in body and array indexes: it stops at the
// first free-form key (an additionalProperties map such as
// distance_factors), which the client chose and is never sent back.
func fieldsOf(err error, body *openapi3.Schema) []string {
	var fields []string
	var walk func(error)
	walk = func(err error) {
		// The concrete type first: a parameter's RequestError wraps a
		// MultiError (several broken bounds), which errors.As would reach
		// before the parameter's name.
		switch e := err.(type) {
		case openapi3.MultiError:
			for _, inner := range e {
				walk(inner)
			}
		case *openapi3filter.RequestError:
			if e.Parameter != nil {
				fields = append(fields, e.Parameter.Name)
				return
			}
			walk(e.Err) // the body
		case *openapi3.SchemaError:
			if path := declaredPath(body, e.JSONPointer()); path != "" {
				fields = append(fields, path)
			}
		case interface{ Unwrap() []error }: // errors.Join
			for _, inner := range e.Unwrap() {
				walk(inner)
			}
		default:
			if inner := errors.Unwrap(err); inner != nil {
				walk(inner)
			}
		}
	}
	walk(err)
	slices.Sort(fields)
	return slices.Compact(fields)
}

// declaredPath joins the leading segments of path that schema declares: a
// property name, or an index of an array. A composed schema (allOf, oneOf,
// anyOf) declares no properties at its own level, so the path stops there:
// safe, but the field is then not named.
func declaredPath(schema *openapi3.Schema, path []string) string {
	var kept []string
	for _, seg := range path {
		if schema == nil {
			break
		}
		if prop, ok := schema.Properties[seg]; ok && prop.Value != nil {
			kept, schema = append(kept, seg), prop.Value
			continue
		}
		if _, err := strconv.Atoi(seg); err == nil && schema.Items != nil && schema.Items.Value != nil {
			kept, schema = append(kept, seg), schema.Items.Value
			continue
		}
		break
	}
	return strings.Join(kept, ".")
}
