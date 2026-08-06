package domain

import (
	"errors"
	"fmt"
)

// FieldError preserves the input field responsible for a typed validation failure while keeping
// the underlying domain error available to errors.Is callers.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string {
	if e == nil || e.Err == nil {
		return "invalid field"
	}
	return fmt.Sprintf("%s: %v", e.Field, e.Err)
}

func (e *FieldError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewFieldError(field string, err error) error {
	return &FieldError{Field: field, Err: err}
}

func FieldName(err error) string {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		return fieldErr.Field
	}
	return ""
}

var (
	// ErrNotImplemented marks a declared boundary whose production dependencies are not wired yet.
	ErrNotImplemented = errors.New("not_implemented")
	// ErrInvalidArgument reports malformed or semantically invalid caller input.
	ErrInvalidArgument = errors.New("invalid_argument")
	// ErrNotFound reports that the requested resource does not exist in the caller's scope.
	ErrNotFound = errors.New("not_found")
	// ErrConflict reports that the requested transition conflicts with current state.
	ErrConflict = errors.New("conflict")
	// ErrUnauthorized reports missing or invalid authentication context.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden reports authenticated access outside the caller's ownership boundary.
	ErrForbidden = errors.New("forbidden")
	// ErrRateLimited reports a caller that must wait before retrying a bounded operation.
	ErrRateLimited = errors.New("rate_limited")
)
