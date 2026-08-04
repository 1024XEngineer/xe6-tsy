package domain

import (
	"errors"
	"testing"
)

func TestFieldErrorPreservesCauseAndField(t *testing.T) {
	err := NewFieldError("limit", ErrInvalidArgument)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("errors.Is() = false, want true")
	}
	if got := FieldName(err); got != "limit" {
		t.Fatalf("FieldName() = %q, want %q", got, "limit")
	}
}
