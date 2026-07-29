package recordstore

import (
	"context"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

type participantReader interface {
	List(context.Context, string, string, recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error)
}

type participantWriter interface {
	Update(context.Context, string, string, participants.Update) (recordsv1.Participant, error)
	FindOrCreate(context.Context, recordsv1.SpeakerObservation) (recordsv1.Participant, error)
}

// ParticipantRepository composes the participant read and write capabilities
// required by participants.Service without widening the recordstore boundary.
type ParticipantRepository struct {
	reader participantReader
	writer participantWriter
}

func NewParticipantRepository(reader participantReader, writer participantWriter) (*ParticipantRepository, error) {
	if reader == nil || writer == nil {
		return nil, fmt.Errorf("create participant repository: reader and writer are required")
	}
	return &ParticipantRepository{reader: reader, writer: writer}, nil
}

func (r *ParticipantRepository) List(
	ctx context.Context,
	accountID string,
	sessionID string,
	query recordsv1.ListParticipantsQuery,
) (recordsv1.ParticipantListResponse, error) {
	return r.reader.List(ctx, accountID, sessionID, query)
}

func (r *ParticipantRepository) Update(
	ctx context.Context,
	sessionID string,
	participantID string,
	update participants.Update,
) (recordsv1.Participant, error) {
	return r.writer.Update(ctx, sessionID, participantID, update)
}

func (r *ParticipantRepository) FindOrCreate(
	ctx context.Context,
	observation recordsv1.SpeakerObservation,
) (recordsv1.Participant, error) {
	return r.writer.FindOrCreate(ctx, observation)
}

type turnReader interface {
	ListSession(context.Context, string, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error)
	Find(context.Context, string, string) (recordsv1.VoiceTurn, error)
	ListHistory(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error)
}

type turnWriter interface {
	StoreFinalTurn(context.Context, recordsv1.FinalTurnEvent) error
	CorrectAttribution(context.Context, turns.AttributionUpdate) (recordsv1.VoiceTurn, error)
}

// TurnRepository composes the turn read and write capabilities required by
// turns.Service. FinalTurnReader intentionally receives TurnReadRepository
// directly and does not depend on this broader service repository.
type TurnRepository struct {
	reader turnReader
	writer turnWriter
}

func NewTurnRepository(reader turnReader, writer turnWriter) (*TurnRepository, error) {
	if reader == nil || writer == nil {
		return nil, fmt.Errorf("create turn repository: reader and writer are required")
	}
	return &TurnRepository{reader: reader, writer: writer}, nil
}

func (r *TurnRepository) StoreFinalTurn(ctx context.Context, event recordsv1.FinalTurnEvent) error {
	return r.writer.StoreFinalTurn(ctx, event)
}

func (r *TurnRepository) ListSession(
	ctx context.Context,
	accountID string,
	sessionID string,
	query recordsv1.ListTurnsQuery,
) (recordsv1.VoiceTurnListResponse, error) {
	return r.reader.ListSession(ctx, accountID, sessionID, query)
}

func (r *TurnRepository) Find(ctx context.Context, accountID string, turnID string) (recordsv1.VoiceTurn, error) {
	return r.reader.Find(ctx, accountID, turnID)
}

func (r *TurnRepository) ListHistory(
	ctx context.Context,
	accountID string,
	query recordsv1.ListTurnsQuery,
) (recordsv1.VoiceTurnListResponse, error) {
	return r.reader.ListHistory(ctx, accountID, query)
}

func (r *TurnRepository) CorrectAttribution(
	ctx context.Context,
	update turns.AttributionUpdate,
) (recordsv1.VoiceTurn, error) {
	return r.writer.CorrectAttribution(ctx, update)
}

var (
	_ participants.Repository = (*ParticipantRepository)(nil)
	_ turns.Repository        = (*TurnRepository)(nil)
)
