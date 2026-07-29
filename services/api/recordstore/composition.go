package recordstore

import (
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ServiceComposition contains the records services and the validated final-turn reader used by
// downstream delivery. The database pool and session adapters remain owned by the composition root.
type ServiceComposition struct {
	Participants    *participants.Service
	Turns           *turns.Service
	FinalTurns      *turns.FinalTurnReader
	FinalTurnWorker *turns.FinalTurnWorker
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

	turnService := turns.NewService(turnRepository, sessionOwner, nil)
	finalTurnOutbox := NewFinalTurnOutbox(pool)
	return &ServiceComposition{
		Participants:    participants.NewService(participantRepository, sessionOwner, nil),
		Turns:           turnService,
		FinalTurns:      turns.NewFinalTurnReader(turnReader),
		FinalTurnWorker: turns.NewFinalTurnWorker(finalTurnOutbox, turns.NewFinalTurnHandler(turnService)),
	}, nil
}
