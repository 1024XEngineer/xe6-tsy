package domain

import "errors"

var (
	ErrNotImplemented  = errors.New("not_implemented")
	ErrInvalidArgument = errors.New("invalid_argument")
	ErrNotFound        = errors.New("not_found")
	ErrConflict        = errors.New("conflict")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
)
