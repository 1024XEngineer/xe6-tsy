package localruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresSpeakerReaderValidation(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name        string
		reader      PostgresSpeakerReader
		ctx         context.Context
		observation recordsv1.SpeakerObservation
		wantErr     string
	}{
		{
			name:        "canceled context",
			reader:      PostgresSpeakerReader{},
			ctx:         canceled,
			observation: recordsv1.SpeakerObservation{SessionID: "s1", TurnID: "t1"},
			wantErr:     context.Canceled.Error(),
		},
		{
			name:        "missing session_id",
			reader:      PostgresSpeakerReader{},
			ctx:         context.Background(),
			observation: recordsv1.SpeakerObservation{TurnID: "t1"},
			wantErr:     "session_id and turn_id are required",
		},
		{
			name:        "missing turn_id",
			reader:      PostgresSpeakerReader{},
			ctx:         context.Background(),
			observation: recordsv1.SpeakerObservation{SessionID: "s1"},
			wantErr:     "session_id and turn_id are required",
		},
		{
			name:        "nil pool without beginTx",
			reader:      PostgresSpeakerReader{},
			ctx:         context.Background(),
			observation: recordsv1.SpeakerObservation{SessionID: "s1", TurnID: "t1", ProviderSpeakerID: "mic-a"},
			wantErr:     "postgres speaker reader pool is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := tt.reader.GetProvisionalAttribution(tt.ctx, tt.observation)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestScanSpeakerParticipant(t *testing.T) {
	t.Parallel()

	display := "Alice"
	provider := "spk-1"
	profile := "vp-1"
	confidence := 0.9
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("CST", 8*3600))
	updated := created.Add(time.Hour)

	t.Run("maps nullable fields and normalizes UTC", func(t *testing.T) {
		t.Parallel()
		row := &fakeSpeakerRow{values: []any{
			"p1", "s1", "speaker_01", &display, &provider, &profile, &confidence, created, updated,
		}}
		got, err := scanSpeakerParticipant(row)
		if err != nil {
			t.Fatalf("scanSpeakerParticipant() error = %v", err)
		}
		if got.ID != "p1" || got.SessionID != "s1" || got.SpeakerCode != "speaker_01" {
			t.Fatalf("participant identity = %#v", got)
		}
		if got.DisplayName == nil || *got.DisplayName != display {
			t.Fatalf("DisplayName = %v", got.DisplayName)
		}
		if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
			t.Fatalf("timestamps not UTC: created=%v updated=%v", got.CreatedAt.Location(), got.UpdatedAt.Location())
		}
	})

	t.Run("propagates scan error", func(t *testing.T) {
		t.Parallel()
		want := errors.New("scan failed")
		_, err := scanSpeakerParticipant(&fakeSpeakerRow{err: want})
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	})
}

func TestPostgresSpeakerReaderFindOrCreatePaths(t *testing.T) {
	t.Parallel()

	existing := recordsv1.Participant{
		ID:          "p-existing",
		SessionID:   "session-1",
		SpeakerCode: "speaker_01",
		CreatedAt:   time.Unix(1, 0).UTC(),
		UpdatedAt:   time.Unix(2, 0).UTC(),
	}
	inserted := recordsv1.Participant{
		ID:          "p-new",
		SessionID:   "session-1",
		SpeakerCode: "speaker_02",
		CreatedAt:   time.Unix(3, 0).UTC(),
		UpdatedAt:   time.Unix(4, 0).UTC(),
	}

	tests := []struct {
		name       string
		tx         *scriptedSpeakerTx
		beginErr   error
		providerID string
		wantCode   string
		wantPartID string
		wantErr    string
	}{
		{
			name: "returns existing participant",
			tx: &scriptedSpeakerTx{
				find: existing,
			},
			providerID: "mic-a",
			wantCode:   "speaker_01",
			wantPartID: "p-existing",
		},
		{
			name: "inserts when missing",
			tx: &scriptedSpeakerTx{
				findErr:  pgx.ErrNoRows,
				ordinal:  2,
				inserted: inserted,
			},
			providerID: "mic-a",
			wantCode:   "speaker_02",
			wantPartID: "p-new",
		},
		{
			name:       "begin failure",
			beginErr:   errors.New("begin boom"),
			providerID: "mic-a",
			wantErr:    "begin participant allocation",
		},
		{
			name: "lock failure",
			tx: &scriptedSpeakerTx{
				lockErr: errors.New("lock boom"),
			},
			providerID: "mic-a",
			wantErr:    "lock participant session",
		},
		{
			name: "lookup query failure",
			tx: &scriptedSpeakerTx{
				findErr: errors.New("db down"),
			},
			providerID: "mic-a",
			wantErr:    "find participant by provider key",
		},
		{
			name: "count failure after miss",
			tx: &scriptedSpeakerTx{
				findErr:  pgx.ErrNoRows,
				countErr: errors.New("count boom"),
			},
			providerID: "mic-a",
			wantErr:    "allocate participant speaker code",
		},
		{
			name: "insert failure",
			tx: &scriptedSpeakerTx{
				findErr:   pgx.ErrNoRows,
				ordinal:   1,
				insertErr: errors.New("insert boom"),
			},
			providerID: "mic-a",
			wantErr:    "insert participant",
		},
		{
			name: "commit failure on existing lookup",
			tx: &scriptedSpeakerTx{
				find:      existing,
				commitErr: errors.New("commit boom"),
			},
			providerID: "mic-a",
			wantErr:    "commit participant lookup",
		},
		{
			name: "commit failure on insert",
			tx: &scriptedSpeakerTx{
				findErr:   pgx.ErrNoRows,
				ordinal:   1,
				inserted:  inserted,
				commitErr: errors.New("commit boom"),
			},
			providerID: "mic-a",
			wantErr:    "commit participant allocation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := PostgresSpeakerReader{
				beginTx: func(ctx context.Context) (speakerTx, error) {
					if tt.beginErr != nil {
						return nil, tt.beginErr
					}
					return tt.tx, nil
				},
			}
			got, err := reader.GetProvisionalAttribution(context.Background(), recordsv1.SpeakerObservation{
				SessionID:         "session-1",
				TurnID:            "turn-1",
				ProviderSpeakerID: tt.providerID,
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetProvisionalAttribution() error = %v", err)
			}
			if got.SpeakerCode != tt.wantCode || got.ParticipantID == nil || *got.ParticipantID != tt.wantPartID {
				t.Fatalf("attribution = %#v", got)
			}
			if got.AttributionStatus != recordsv1.AttributionProvisional {
				t.Fatalf("AttributionStatus = %q", got.AttributionStatus)
			}
			if tt.tx != nil && !tt.tx.rolledBack {
				t.Fatal("expected deferred Rollback to run")
			}
		})
	}
}

func TestPostgresSpeakerReaderKeepsPendingWithoutProviderKey(t *testing.T) {
	t.Parallel()
	reader := PostgresSpeakerReader{}
	got, err := reader.GetProvisionalAttribution(context.Background(), recordsv1.SpeakerObservation{
		SessionID: "session-1",
		TurnID:    "turn-1",
	})
	if err != nil {
		t.Fatalf("GetProvisionalAttribution() error = %v", err)
	}
	if got.ParticipantID != nil || got.AttributionStatus != recordsv1.AttributionPending {
		t.Fatalf("attribution = %#v, want pending", got)
	}
}

type fakeSpeakerRow struct {
	values []any
	err    error
}

func (r *fakeSpeakerRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("scan arity mismatch")
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = r.values[i].(string)
		case **string:
			if r.values[i] == nil {
				*d = nil
			} else {
				v := r.values[i].(*string)
				*d = v
			}
		case **float64:
			if r.values[i] == nil {
				*d = nil
			} else {
				v := r.values[i].(*float64)
				*d = v
			}
		case *time.Time:
			*d = r.values[i].(time.Time)
		default:
			return errors.New("unsupported scan dest")
		}
	}
	return nil
}

type scriptedSpeakerTx struct {
	lockErr    error
	find       recordsv1.Participant
	findErr    error
	ordinal    int64
	countErr   error
	inserted   recordsv1.Participant
	insertErr  error
	commitErr  error
	rolledBack bool
	onFindArgs func(sessionID, providerID string)
}

func (t *scriptedSpeakerTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_advisory_xact_lock") {
		return pgconn.CommandTag{}, t.lockErr
	}
	return pgconn.CommandTag{}, nil
}

func (t *scriptedSpeakerTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "FROM voice_session_participants") && strings.Contains(sql, "provider_speaker_id"):
		if t.onFindArgs != nil && len(args) >= 2 {
			t.onFindArgs(args[0].(string), args[1].(string))
		}
		if t.findErr != nil {
			return &fakeSpeakerRow{err: t.findErr}
		}
		return participantRow(t.find)
	case strings.Contains(sql, "COUNT(*)"):
		if t.countErr != nil {
			return &fakeSpeakerRow{err: t.countErr}
		}
		return &countRow{value: t.ordinal}
	case strings.Contains(sql, "INSERT INTO voice_session_participants"):
		if t.insertErr != nil {
			return &fakeSpeakerRow{err: t.insertErr}
		}
		return participantRow(t.inserted)
	default:
		return &fakeSpeakerRow{err: errors.New("unexpected query: " + sql)}
	}
}

func (t *scriptedSpeakerTx) Commit(context.Context) error { return t.commitErr }

func (t *scriptedSpeakerTx) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

func participantRow(p recordsv1.Participant) *fakeSpeakerRow {
	return &fakeSpeakerRow{values: []any{
		p.ID, p.SessionID, p.SpeakerCode, p.DisplayName, p.ProviderSpeakerID,
		p.VoiceProfileID, p.Confidence, p.CreatedAt, p.UpdatedAt,
	}}
}

type countRow struct {
	value int64
	err   error
}

func (r *countRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("count scan arity")
	}
	ptr, ok := dest[0].(*int64)
	if !ok {
		return errors.New("count scan type")
	}
	*ptr = r.value
	return nil
}
