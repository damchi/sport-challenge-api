package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
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
			RespondError(c, logger, apperr.InvalidFields(err, fieldsOf(err)...))
		}
	}, nil
}

// fieldsOf lists, sorted and once each, the fields an error from
// openapi3filter.ValidateRequest is about: the parameter name, or the path of
// the body property ("pseudo", "items.0.distance"). The path is made of the
// contract's property names and of array indexes; a schema with free-form
// keys (additionalProperties with a schema) would add the client's keys to
// it, so such a schema needs a filter here first.
func fieldsOf(err error) []string {
	var fields []string
	var walk func(error)
	walk = func(err error) {
		var multi openapi3.MultiError
		var reqErr *openapi3filter.RequestError
		var schemaErr *openapi3.SchemaError
		switch {
		case errors.As(err, &multi):
			for _, e := range multi {
				walk(e)
			}
		case errors.As(err, &reqErr) && reqErr.Parameter != nil:
			fields = append(fields, reqErr.Parameter.Name)
		case errors.As(err, &schemaErr):
			if path := strings.Join(schemaErr.JSONPointer(), "."); path != "" {
				fields = append(fields, path)
			}
		}
	}
	walk(err)
	slices.Sort(fields)
	return slices.Compact(fields)
}
