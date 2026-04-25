// Package apperr defines the sentinel and coded errors shared across the
// service and handler layers.
//
// There are two layers:
//
//  1. Sentinels (ErrNotFound, ErrForbidden, ErrInvalidInput) — broad categories
//     the service layer returns when the detail doesn't matter. Handlers map
//     them to default HTTP responses (404/403/400) with a generic business code.
//
//  2. Coded errors (*AppError) — carry a stable machine-readable code, a
//     client-safe message, optional details, and the exact HTTP status. They
//     wrap one of the sentinels so `errors.Is(err, ErrNotFound)` etc. keep
//     working. Handlers pull the code/details via `errors.As`.
//
// Services should prefer the coded constructors (TodoNotFound, InvalidTransition,
// …) so error responses carry the right enum. Falling back to a bare sentinel
// still yields a sensible HTTP response, it just loses the specific code.
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
//
// It wraps one of the sentinels so coarse `errors.Is` checks in business code
// continue to work regardless of which specific code was returned.
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

func (e *AppError) Unwrap() error            { return e.wrapped }
func (e *AppError) Code() string             { return e.code }
func (e *AppError) Message() string          { return e.message }
func (e *AppError) Details() map[string]any  { return e.details }
func (e *AppError) HTTPStatus() int          { return e.status }

// InvalidInput wraps ErrInvalidInput with a client-safe message. Kept for
// backwards-compatibility with existing service code; new call sites should
// prefer ValidationError for the stable VALIDATION_ERROR code.
func InvalidInput(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, msg)
}

// ValidationError returns a 400 VALIDATION_ERROR. `field` may be empty when the
// problem isn't tied to a single field.
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

// TodoNotFound / GoalNotFound / AssigneeNotFound render as 404 with the matching
// resource-specific code. Cross-space access uses these same codes — callers
// must not distinguish "wrong space" from "does not exist".
func TodoNotFound() *AppError {
	return &AppError{code: "TODO_NOT_FOUND", message: "todo not found", status: http.StatusNotFound, wrapped: ErrNotFound}
}
func GoalNotFound() *AppError {
	return &AppError{code: "GOAL_NOT_FOUND", message: "goal not found", status: http.StatusNotFound, wrapped: ErrNotFound}
}
func AssigneeNotFound() *AppError {
	return &AppError{code: "ASSIGNEE_NOT_FOUND", message: "assignee not found", status: http.StatusNotFound, wrapped: ErrNotFound}
}

// Forbidden is the catch-all 403 when a coarser code is enough. Prefer the
// specific constructors (SpaceForbidden, GoalMemberRequired) when the reason
// matters to the client.
func Forbidden(msg string) *AppError {
	if msg == "" {
		msg = "forbidden"
	}
	return &AppError{code: "FORBIDDEN", message: msg, status: http.StatusForbidden, wrapped: ErrForbidden}
}

// SpaceForbidden renders as 403 SPACE_FORBIDDEN — the caller is authenticated
// but is not a member of the target Space. Reserved for when the auth SDK is
// wired; handlers currently return TODO_NOT_FOUND / GOAL_NOT_FOUND for cross-
// space access to avoid leaking tenancy.
func SpaceForbidden() *AppError {
	return &AppError{code: "SPACE_FORBIDDEN", message: "not a member of this space", status: http.StatusForbidden, wrapped: ErrForbidden}
}

// InvalidTransition renders the 400 INVALID_TRANSITION with the exact allowed
// targets so clients don't need a second GET to learn what's permitted.
// `allowed` may be a `[]model.TodoStatus` or a `[]string`; it is always
// serialised as a JSON array of strings in the response.
func InvalidTransition(current string, allowed []string) *AppError {
	return &AppError{
		code:    "INVALID_TRANSITION",
		message: fmt.Sprintf("cannot transition from %q to requested status", current),
		details: map[string]any{"current": current, "allowed": allowed},
		status:  http.StatusBadRequest,
		wrapped: ErrInvalidInput,
	}
}

// DuplicateAssignee is 409 when inserting an assignee whose (todo_id, user_id)
// already exists. Repositories should detect the unique-key violation and map
// to this before the error reaches the handler layer.
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
