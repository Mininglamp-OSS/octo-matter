// Package apperr defines sentinel errors shared across the service and handler layers.
// Repositories translate driver-level "no rows" into ErrNotFound so handlers can map
// to HTTP 404 without leaking SQL details. Services return ErrForbidden for authz
// failures and ErrInvalidInput (via InvalidInput) for client-fixable input errors.
package apperr

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
)

// InvalidInput wraps ErrInvalidInput with a client-safe message. Handlers
// unwrap with errors.Is(err, ErrInvalidInput) and render the message verbatim
// to the client — callers must NOT include internal details in the message.
func InvalidInput(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, msg)
}
