package recordstore

import (
	"errors"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrInvalidCursor marks a malformed, tampered, expired-version, or wrong-scope record cursor.
var ErrInvalidCursor = fmt.Errorf("%w: record cursor", domain.ErrInvalidArgument)

// MapError converts PostgreSQL storage failures into the domain errors shared by service callers.
// Foreign-key violations represent a referenced record that does not exist in the requested scope.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	}

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}

	switch postgresError.Code {
	case "23505":
		return fmt.Errorf("%w: %w", domain.ErrConflict, err)
	case "23503":
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	default:
		return err
	}
}
