// Package apperr defines the sentinel and coded errors shared across the
// service and handler layers.
//
// User-facing text is carried as an i18n message key (msgID) plus optional
// template params, not as a literal string. The handler layer localizes the
// key against the request language when rendering the REST envelope:
//
//	{"error":{"code":<Code>, "message":<localized>, "details":<Details>}}
package apperr

import (
	"errors"
	"net/http"

	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
)

// AppError carries the fields rendered into the REST error envelope. msgID is
// an i18n key resolved at render time; params feed its template placeholders.
type AppError struct {
	code    string
	msgID   string
	params  map[string]any
	details map[string]any
	status  int
	wrapped error
}

// Error returns the (unlocalized) message key, for logs and errors.Is chains.
func (e *AppError) Error() string {
	if e.msgID != "" {
		return e.code + ": " + e.msgID
	}
	return e.code
}

func (e *AppError) Unwrap() error           { return e.wrapped }
func (e *AppError) Code() string            { return e.code }
func (e *AppError) MessageID() string       { return e.msgID }
func (e *AppError) Params() map[string]any  { return e.params }
func (e *AppError) Details() map[string]any { return e.details }
func (e *AppError) HTTPStatus() int         { return e.status }

// InvalidInput returns a 400 VALIDATION_ERROR carrying the given message key.
func InvalidInput(msgID string) *AppError {
	return &AppError{code: "VALIDATION_ERROR", msgID: msgID, status: http.StatusBadRequest, wrapped: ErrInvalidInput}
}

// ValidationError returns a 400 VALIDATION_ERROR with an optional field detail.
func ValidationError(msgID, field string) *AppError {
	return ValidationErrorP(msgID, field, nil)
}

// ValidationErrorP is ValidationError with template params for the message key.
func ValidationErrorP(msgID, field string, params map[string]any) *AppError {
	var details map[string]any
	if field != "" {
		details = map[string]any{"field": field}
	}
	return &AppError{
		code:    "VALIDATION_ERROR",
		msgID:   msgID,
		params:  params,
		details: details,
		status:  http.StatusBadRequest,
		wrapped: ErrInvalidInput,
	}
}

// MatterNotFound renders as 404 MATTER_NOT_FOUND.
func MatterNotFound() *AppError {
	return &AppError{code: "MATTER_NOT_FOUND", msgID: i18n.KeyMatterNotFound, status: http.StatusNotFound, wrapped: ErrNotFound}
}

// AssigneeNotFound renders as 404 ASSIGNEE_NOT_FOUND.
func AssigneeNotFound() *AppError {
	return &AppError{code: "ASSIGNEE_NOT_FOUND", msgID: i18n.KeyAssigneeNotFound, status: http.StatusNotFound, wrapped: ErrNotFound}
}

// Forbidden is the catch-all 403. An empty msgID falls back to the generic key.
func Forbidden(msgID string) *AppError {
	if msgID == "" {
		msgID = i18n.KeyForbidden
	}
	return &AppError{code: "FORBIDDEN", msgID: msgID, status: http.StatusForbidden, wrapped: ErrForbidden}
}

// SpaceForbidden renders as 403 SPACE_FORBIDDEN.
func SpaceForbidden() *AppError {
	return &AppError{code: "SPACE_FORBIDDEN", msgID: i18n.KeySpaceForbidden, status: http.StatusForbidden, wrapped: ErrForbidden}
}

// Upstream renders as 503 UPSTREAM_UNAVAILABLE. Use when a synchronous
// dependency (e.g. octoim) is unreachable or returns 5xx — the request can
// be retried, but the failure is not the caller's fault.
func Upstream(msgID string) *AppError {
	if msgID == "" {
		msgID = i18n.KeyUpstream
	}
	return &AppError{code: "UPSTREAM_UNAVAILABLE", msgID: msgID, status: http.StatusServiceUnavailable}
}

// RateLimited renders as 429 RATE_LIMITED. Use when a per-resource cooldown
// (e.g. LLM-backed timeline write) rejects the request.
func RateLimited(msgID string) *AppError {
	if msgID == "" {
		msgID = i18n.KeyRateLimited
	}
	return &AppError{code: "RATE_LIMITED", msgID: msgID, status: http.StatusTooManyRequests}
}

// DuplicateAssignee is 409 when inserting an assignee whose (matter_id, user_id)
// already exists.
func DuplicateAssignee() *AppError {
	return &AppError{code: "DUPLICATE_ASSIGNEE", msgID: i18n.KeyDuplicateAssignee, status: http.StatusConflict, wrapped: ErrInvalidInput}
}

// AsAppError is a convenience wrapper around errors.As.
func AsAppError(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
