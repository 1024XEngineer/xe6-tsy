package recordstore

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name  string
		input error
		want  error
	}{
		{name: "no rows", input: pgx.ErrNoRows, want: domain.ErrNotFound},
		{name: "unique violation", input: &pgconn.PgError{Code: "23505"}, want: domain.ErrConflict},
		{name: "foreign key violation", input: &pgconn.PgError{Code: "23503"}, want: domain.ErrNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := MapError(test.input); !errors.Is(err, test.want) {
				t.Fatalf("MapError() error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}

func TestMapErrorLeavesUnknownErrorUntouched(t *testing.T) {
	input := errors.New("connection reset")
	if got := MapError(input); got != input {
		t.Fatalf("MapError() = %v, want original error %v", got, input)
	}
}
