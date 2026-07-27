package domain

import "errors"

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
)
