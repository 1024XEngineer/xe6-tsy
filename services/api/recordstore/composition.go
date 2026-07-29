package recordstore

import (
	"context"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ServiceComposition contains the records services and the validated final-turn reader used by
// downstream delivery. The database pool and session adapters remain owned by the composition root.
type ServiceComposition struct {
	Participants *participants.Service
	Turns        *turns.Service
	FinalTurns   *turns.FinalTurnReader
}

// NewServices composes records domain services over the PostgreSQL read/write adapters.
// SessionOwner and SessionScope are separate because the records SQL path requires the complete
// account session set while service authorization requires one session owner lookup.
func NewServices(
	pool *pgxpool.Pool,
	cursorSigningKey []byte,
	sessionOwner recordsv1.SessionOwnerReader,
	sessionScope AccountSessionScopeReader,
) (*ServiceComposition, error) {
	if pool == nil {
		return nil, fmt.Errorf("create records services: PostgreSQL pool is required")
	}
	cursors, err := serviceCursorCodec(cursorSigningKey, sessionOwner, sessionScope)
	if err != nil {
		return nil, err
	}
	return composeServices(pool, cursors, sessionOwner, sessionScope)
}

func serviceCursorCodec(
	cursorSigningKey []byte,
	sessionOwner recordsv1.SessionOwnerReader,
	sessionScope AccountSessionScopeReader,
) (*CursorCodec, error) {
	if sessionOwner == nil {
		return nil, fmt.Errorf("create records services: session owner reader is required")
	}
	if sessionScope == nil {
		return nil, fmt.Errorf("create records services: session scope reader is required")
	}

	cursors, err := NewCursorCodec(cursorSigningKey)
	if err != nil {
		return nil, fmt.Errorf("create records services: %w", err)
	}
	return cursors, nil
}

func composeServices(
	pool *pgxpool.Pool,
	cursors *CursorCodec,
	sessionOwner recordsv1.SessionOwnerReader,
	sessionScope AccountSessionScopeReader,
) (*ServiceComposition, error) {
	participantReader, err := NewParticipantReadRepository(pool, cursors, sessionScope)
	if err != nil {
		return nil, fmt.Errorf("create records services: %w", err)
	}
	participantRepository, err := NewParticipantRepository(participantReader, NewParticipantWriter(pool))
	if err != nil {
		return nil, fmt.Errorf("create records services: %w", err)
	}

	turnReader, err := NewTurnReadRepository(pool, cursors, sessionScope)
	if err != nil {
		return nil, fmt.Errorf("create records services: %w", err)
	}
	turnRepository, err := NewTurnRepository(turnReader, NewTurnWriter(pool))
	if err != nil {
		return nil, fmt.Errorf("create records services: %w", err)
	}

	return &ServiceComposition{
		Participants: participants.NewService(participantRepository, sessionOwner, nil),
		Turns:        turns.NewService(turnRepository, sessionOwner, nil),
		FinalTurns:   turns.NewFinalTurnReader(turnReader),
	}, nil
}

// OpenServices opens the records PostgreSQL pool, applies recordstore migrations, and composes
// the services. The cleanup function closes the pool after the caller has stopped its consumers.
func OpenServices(
	ctx context.Context,
	databaseURL string,
	cursorSigningKey []byte,
	sessionOwner recordsv1.SessionOwnerReader,
	sessionScope AccountSessionScopeReader,
) (*ServiceComposition, func(), error) {
	cursors, err := serviceCursorCodec(cursorSigningKey, sessionOwner, sessionScope)
	if err != nil {
		return nil, nil, err
	}

	pool, err := Open(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}

	services, err := composeServices(pool, cursors, sessionOwner, sessionScope)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return services, pool.Close, nil
}
