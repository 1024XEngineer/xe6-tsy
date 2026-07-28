package webapi

import (
	"context"
	"errors"
	"log/slog"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

var errNotImplemented = errors.New("voice record persistence is not implemented")

// NewNotImplementedHandler returns a route-ready records adapter for application composition.
// It exposes the public paths as 501 until production persistence adapters are supplied.
func NewNotImplementedHandler(logger *slog.Logger) *Server {
	adapters := notImplementedAdapters{}
	return NewHandler(Dependencies{
		Participants: participants.NewService(adapters, adapters, nil),
		Turns:        turns.NewService(adapters, adapters, nil),
		Accounts:     ContextAccountProvider{},
		System:       ContextSystemAuthorizer{},
		Logger:       logger,
	})
}

type notImplementedAdapters struct{}

func (notImplementedAdapters) AccountIDForSession(context.Context, string) (string, error) {
	return "", errNotImplemented
}

func (notImplementedAdapters) List(context.Context, string, recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	return recordsv1.ParticipantListResponse{}, errNotImplemented
}

func (notImplementedAdapters) Update(context.Context, string, string, participants.Update) (recordsv1.Participant, error) {
	return recordsv1.Participant{}, errNotImplemented
}

func (notImplementedAdapters) FindOrCreate(context.Context, recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	return recordsv1.Participant{}, errNotImplemented
}

func (notImplementedAdapters) StoreFinalTurn(context.Context, recordsv1.FinalTurnEvent) error {
	return errNotImplemented
}

func (notImplementedAdapters) ListSession(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return recordsv1.VoiceTurnListResponse{}, errNotImplemented
}

func (notImplementedAdapters) Find(context.Context, string) (recordsv1.VoiceTurn, error) {
	return recordsv1.VoiceTurn{}, errNotImplemented
}

func (notImplementedAdapters) ListHistory(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return recordsv1.VoiceTurnListResponse{}, errNotImplemented
}

func (notImplementedAdapters) CorrectAttribution(context.Context, turns.AttributionUpdate) (recordsv1.VoiceTurn, error) {
	return recordsv1.VoiceTurn{}, errNotImplemented
}

func (notImplementedAdapters) ReadFinalTurns(context.Context, string, []string) ([]recordsv1.FinalTurnSnapshot, error) {
	return nil, errNotImplemented
}

var _ participants.Repository = notImplementedAdapters{}
var _ turns.Repository = notImplementedAdapters{}
var _ recordsv1.SessionOwnerReader = notImplementedAdapters{}
