package delivery

import (
	"context"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// RecordsTurnReader converts the records provider contract into delivery-owned snapshots. It
// copies nullable fields so later attribution updates or caller mutations cannot alter an already
// assembled delivery value through shared pointers.
type RecordsTurnReader struct {
	reader recordsv1.TurnReader
}

// NewRecordsTurnReader binds the account-scoped records provider used during message assembly.
func NewRecordsTurnReader(reader recordsv1.TurnReader) *RecordsTurnReader {
	return &RecordsTurnReader{reader: reader}
}

// ReadFinalTurns preserves the provider's validated order while crossing the module type boundary.
func (reader *RecordsTurnReader) ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]FinalTurnSnapshot, error) {
	snapshots, err := reader.reader.ReadFinalTurns(ctx, accountID, turnIDs)
	if err != nil {
		return nil, err
	}

	deliverySnapshots := make([]FinalTurnSnapshot, len(snapshots))
	for index, snapshot := range snapshots {
		deliverySnapshots[index] = FinalTurnSnapshot{
			TurnID:                snapshot.TurnID,
			SessionID:             snapshot.SessionID,
			ParticipantID:         copyOptionalString(snapshot.ParticipantID),
			SpeakerLabelSnapshot:  copyOptionalString(snapshot.SpeakerLabelSnapshot),
			SourceLanguage:        snapshot.SourceLanguage,
			TargetLanguage:        snapshot.TargetLanguage,
			LanguageConfigVersion: snapshot.LanguageConfigVersion,
			SourceText:            snapshot.SourceText,
			TranslatedText:        snapshot.TranslatedText,
			CreatedAt:             snapshot.CreatedAt,
		}
	}
	return deliverySnapshots, nil
}

func copyOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ TurnReader = (*RecordsTurnReader)(nil)
