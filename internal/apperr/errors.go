// Package apperr defines sentinel errors shared across the service and handler layers.
// Repositories translate driver-level "no rows" into ErrNotFound so handlers can map
// to HTTP 404 without leaking SQL details. Services return ErrForbidden for authz
// failures; handlers map to 403.
package apperr

import "errors"

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)
