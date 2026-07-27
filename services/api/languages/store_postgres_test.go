package languages

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapInsertUniqueViolation(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		want       error
		wantMapped bool
	}{
		{
			name:       "idempotency_index",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "idx_lang_config_idempotency"},
			want:       ErrIdempotencyConflict,
			wantMapped: true,
		},
		{
			name:       "active_index",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "idx_lang_config_active"},
			want:       ErrVersionConflict,
			wantMapped: true,
		},
		{
			name:       "version_index",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "idx_lang_config_version"},
			want:       ErrVersionConflict,
			wantMapped: true,
		},
		{
			name:       "unknown_unique",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "voice_session_language_configs_pkey"},
			want:       ErrVersionConflict,
			wantMapped: true,
		},
		{
			name:       "non_unique",
			err:        &pgconn.PgError{Code: "23503", ConstraintName: "idx_lang_config_idempotency"},
			wantMapped: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapInsertUniqueViolation(tc.err)
			if !tc.wantMapped {
				if got != nil {
					t.Fatalf("mapped = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("mapped = %v, want %v", got, tc.want)
			}
		})
	}
}
