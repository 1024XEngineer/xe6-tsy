package modeprojection

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresRepositoryRejectsMissingDependencies(t *testing.T) {
	var repository *PostgresRepository
	if err := repository.Project(t.Context(), consumerEvent()); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("nil Project() error = %v, want not implemented", err)
	}
	if _, err := repository.Latest(t.Context(), "session-1"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("nil Latest() error = %v, want not implemented", err)
	}
	repository = NewPostgresRepository(nil)
	if err := repository.Project(t.Context(), consumerEvent()); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("missing pool Project() error = %v, want not implemented", err)
	}
	if _, err := repository.Latest(t.Context(), "session-1"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("missing pool Latest() error = %v, want not implemented", err)
	}
}

func TestPostgresRepositoryValidatesBeforeOpeningDatabase(t *testing.T) {
	repository := NewPostgresRepository(nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := repository.Project(ctx, consumerEvent()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Project() error = %v, want context canceled", err)
	}
	if _, err := repository.Latest(t.Context(), ""); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("missing pool Latest(empty) error = %v, want not implemented", err)
	}
}

func TestPostgresErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "no rows", err: pgx.ErrNoRows, want: domain.ErrNotFound},
		{name: "duplicate", err: &pgconn.PgError{Code: "23505"}, want: domain.ErrConflict},
		{name: "foreign key", err: &pgconn.PgError{Code: "23503"}, want: domain.ErrNotFound},
		{name: "check", err: &pgconn.PgError{Code: "23514"}, want: domain.ErrInvalidArgument},
		{name: "numeric", err: &pgconn.PgError{Code: "22003"}, want: domain.ErrInvalidArgument},
		{name: "syntax", err: &pgconn.PgError{Code: "22P02"}, want: domain.ErrInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := mapError(test.err); !errors.Is(err, test.want) {
				t.Fatalf("mapError() = %v, want %v", err, test.want)
			}
		})
	}
	if mapError(nil) != nil {
		t.Fatal("mapError(nil) != nil")
	}
}
