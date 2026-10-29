package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError aborts the request with the single error format of the API:
// {"error": {"code", "message", "request_id"}}. The code is stable and is what
// clients translate; the message is only an English fallback.
func WriteError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, errorResponse{Error: errorBody{
		Code:      code,
		Message:   message,
		RequestID: RequestID(c),
	}})
}

// InternalErrorHandler logs err and answers a generic 500 internal_error: the
// cause is never sent to the client. If the response has already started, it
// only logs, so the body is never corrupted by a second document.
func InternalErrorHandler(logger *slog.Logger) func(*gin.Context, error) {
	return func(c *gin.Context, err error) {
		logger.ErrorContext(c.Request.Context(), "request failed",
			slog.String("route", c.FullPath()),
			slog.String("request_id", RequestID(c)),
			slog.String("error", err.Error()),
		)
		if c.Writer.Written() {
			c.Abort()
			return
		}
		WriteError(c, http.StatusInternalServerError, "internal_error", "Internal server error")
	}
}

// InvalidParamHandler answers a generic validation_failed with the status the
// generated router chose when a path, query or header parameter is invalid.
// The binding error, which may echo the client's value, is not sent back.
func InvalidParamHandler(c *gin.Context, _ error, status int) {
	WriteError(c, status, "validation_failed", "Invalid request")
}

// BadRequestHandler answers a generic 400 validation_failed when a request
// cannot be decoded. The decoding error is not echoed back.
func BadRequestHandler(c *gin.Context, _ error) {
	WriteError(c, http.StatusBadRequest, "validation_failed", "Invalid request")
}
