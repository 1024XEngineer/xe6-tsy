package recordstore

import (
	"context"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestNewParticipantRepositoryRequiresBothCapabilities(t *testing.T) {
	writer := &participantWriterFake{}
	reader := &participantReaderFake{}

	if _, err := NewParticipantRepository(nil, writer); err == nil {
		t.Fatal("NewParticipantRepository(nil, writer) returned nil error")
	}
	if _, err := NewParticipantRepository(reader, nil); err == nil {
		t.Fatal("NewParticipantRepository(reader, nil) returned nil error")
	}
}

func TestParticipantRepositoryDelegatesToReadAndWriteCapabilities(t *testing.T) {
	wantParticipant := recordsv1.Participant{ID: "participant_01"}
	reader := &participantReaderFake{
		response: recordsv1.ParticipantListResponse{Items: []recordsv1.Participant{wantParticipant}},
	}
	writer := &participantWriterFake{participant: wantParticipant}
	repository, err := NewParticipantRepository(reader, writer)
	if err != nil {
		t.Fatalf("NewParticipantRepository() error = %v", err)
	}

	if _, err := repository.List(t.Context(), "account_01", "session_01", recordsv1.ListParticipantsQuery{}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, err := repository.Update(t.Context(), "session_01", "participant_01", participants.Update{DisplayNameSet: true}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := repository.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{SessionID: "session_01", TurnID: "turn_01"}); err != nil {
		t.Fatalf("FindOrCreate() error = %v", err)
	}

	if reader.listCalls != 1 || writer.updateCalls != 1 || writer.findOrCreateCalls != 1 {
		t.Fatalf("delegation calls = reader.List %d, writer.Update %d, writer.FindOrCreate %d", reader.listCalls, writer.updateCalls, writer.findOrCreateCalls)
	}
}

func TestNewTurnRepositoryRequiresBothCapabilities(t *testing.T) {
	writer := &turnWriterFake{}
	reader := &turnReaderFake{}

	if _, err := NewTurnRepository(nil, writer); err == nil {
		t.Fatal("NewTurnRepository(nil, writer) returned nil error")
	}
	if _, err := NewTurnRepository(reader, nil); err == nil {
		t.Fatal("NewTurnRepository(reader, nil) returned nil error")
	}
}

func TestTurnRepositoryDelegatesToReadAndWriteCapabilities(t *testing.T) {
	reader := &turnReaderFake{}
	writer := &turnWriterFake{}
	repository, err := NewTurnRepository(reader, writer)
	if err != nil {
		t.Fatalf("NewTurnRepository() error = %v", err)
	}

	event := recordsv1.FinalTurnEvent{EventID: "event_01"}
	if err := repository.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}
	if _, err := repository.ListSession(t.Context(), "account_01", "session_01", recordsv1.ListTurnsQuery{}); err != nil {
		t.Fatalf("ListSession() error = %v", err)
	}
	if _, err := repository.Find(t.Context(), "account_01", "turn_01"); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if _, err := repository.ListHistory(t.Context(), "account_01", recordsv1.ListTurnsQuery{}); err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	if _, err := repository.CorrectAttribution(t.Context(), turns.AttributionUpdate{TurnID: "turn_01"}); err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}

	if reader.listSessionCalls != 1 || reader.findCalls != 1 || reader.listHistoryCalls != 1 || writer.storeCalls != 1 || writer.correctCalls != 1 {
		t.Fatalf("delegation calls = reader.ListSession %d, reader.Find %d, reader.ListHistory %d, writer.Store %d, writer.Correct %d", reader.listSessionCalls, reader.findCalls, reader.listHistoryCalls, writer.storeCalls, writer.correctCalls)
	}
}

type participantReaderFake struct {
	response  recordsv1.ParticipantListResponse
	listCalls int
}

func (f *participantReaderFake) List(context.Context, string, string, recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	f.listCalls++
	return f.response, nil
}

type participantWriterFake struct {
	participant       recordsv1.Participant
	updateCalls       int
	findOrCreateCalls int
}

func (f *participantWriterFake) Update(context.Context, string, string, participants.Update) (recordsv1.Participant, error) {
	f.updateCalls++
	return f.participant, nil
}

func (f *participantWriterFake) FindOrCreate(context.Context, recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	f.findOrCreateCalls++
	return f.participant, nil
}

type turnReaderFake struct {
	listSessionCalls int
	findCalls        int
	listHistoryCalls int
}

func (f *turnReaderFake) ListSession(context.Context, string, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	f.listSessionCalls++
	return recordsv1.VoiceTurnListResponse{}, nil
}

func (f *turnReaderFake) Find(context.Context, string, string) (recordsv1.VoiceTurn, error) {
	f.findCalls++
	return recordsv1.VoiceTurn{}, nil
}

func (f *turnReaderFake) ListHistory(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	f.listHistoryCalls++
	return recordsv1.VoiceTurnListResponse{}, nil
}

type turnWriterFake struct {
	storeCalls   int
	correctCalls int
}

func (f *turnWriterFake) StoreFinalTurn(context.Context, recordsv1.FinalTurnEvent) error {
	f.storeCalls++
	return nil
}

func (f *turnWriterFake) CorrectAttribution(context.Context, turns.AttributionUpdate) (recordsv1.VoiceTurn, error) {
	f.correctCalls++
	return recordsv1.VoiceTurn{}, nil
}

var (
	_ participantReader = (*participantReaderFake)(nil)
	_ participantWriter = (*participantWriterFake)(nil)
	_ turnReader        = (*turnReaderFake)(nil)
	_ turnWriter        = (*turnWriterFake)(nil)
)
