// Package apperr defines the sentinel and coded errors shared across the
// service and handler layers.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
)

// AppError carries the fields rendered into the REST error envelope:
//
//	{"error":{"code":<Code>, "message":<Message>, "details":<Details>}}
type AppError struct {
	code    string
	message string
	details map[string]any
	status  int
	wrapped error
}

func (e *AppError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("%s: %s", e.code, e.message)
	}
	return e.code
}

func (e *AppError) Unwrap() error           { return e.wrapped }
func (e *AppError) Code() string            { return e.code }
func (e *AppError) Message() string         { return e.message }
func (e *AppError) Details() map[string]any { return e.details }
func (e *AppError) HTTPStatus() int         { return e.status }

// InvalidInput wraps ErrInvalidInput with a client-safe message.
func InvalidInput(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, msg)
}

// ValidationError returns a 400 VALIDATION_ERROR.
func ValidationError(msg, field string) *AppError {
	var details map[string]any
	if field != "" {
		details = map[string]any{"field": field}
	}
	return &AppError{
		code:    "VALIDATION_ERROR",
		message: msg,
		details: details,
		status:  http.StatusBadRequest,
		wrapped: ErrInvalidInput,
	}
}

// MatterNotFound renders as 404 MATTER_NOT_FOUND.
func MatterNotFound() *AppError {
	return &AppError{code: "MATTER_NOT_FOUND", message: "matter not found", status: http.StatusNotFound, wrapped: ErrNotFound}
}

// AssigneeNotFound renders as 404 ASSIGNEE_NOT_FOUND.
func AssigneeNotFound() *AppError {
	return &AppError{code: "ASSIGNEE_NOT_FOUND", message: "assignee not found", status: http.StatusNotFound, wrapped: ErrNotFound}
}

// Forbidden is the catch-all 403.
func Forbidden(msg string) *AppError {
	if msg == "" {
		msg = "forbidden"
	}
	return &AppError{code: "FORBIDDEN", message: msg, status: http.StatusForbidden, wrapped: ErrForbidden}
}

// SpaceForbidden renders as 403 SPACE_FORBIDDEN.
func SpaceForbidden() *AppError {
	return &AppError{code: "SPACE_FORBIDDEN", message: "not a member of this space", status: http.StatusForbidden, wrapped: ErrForbidden}
}

// Upstream renders as 503 UPSTREAM_UNAVAILABLE. Use when a synchronous
// dependency (e.g. octoim) is unreachable or returns 5xx — the request can
// be retried, but the failure is not the caller's fault.
func Upstream(msg string) *AppError {
	if msg == "" {
		msg = "upstream service unavailable"
	}
	return &AppError{code: "UPSTREAM_UNAVAILABLE", message: msg, status: http.StatusServiceUnavailable}
}

// RateLimited renders as 429 RATE_LIMITED. Use when a per-resource cooldown
// (e.g. LLM-backed timeline write) rejects the request.
func RateLimited(msg string) *AppError {
	if msg == "" {
		msg = "please wait before retrying"
	}
	return &AppError{code: "RATE_LIMITED", message: msg, status: http.StatusTooManyRequests}
}

// DuplicateAssignee is 409 when inserting an assignee whose (matter_id, user_id)
// already exists.
func DuplicateAssignee() *AppError {
	return &AppError{code: "DUPLICATE_ASSIGNEE", message: "assignee already exists", status: http.StatusConflict, wrapped: ErrInvalidInput}
}

// AsAppError is a convenience wrapper around errors.As.
func AsAppError(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
