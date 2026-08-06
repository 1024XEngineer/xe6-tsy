package turns

import (
	"context"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// FinalTurnRepository reads account-scoped final-turn snapshots for outbound delivery content.
type FinalTurnRepository interface {
	ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error)
}

// FinalTurnReader validates delivery snapshot reads before crossing the records provider boundary.
type FinalTurnReader struct {
	repository FinalTurnRepository
}

// NewFinalTurnReader binds the repository that enforces account ownership during snapshot reads.
func NewFinalTurnReader(repository FinalTurnRepository) *FinalTurnReader {
	if repository == nil {
		panic("final turn repository is required")
	}
	return &FinalTurnReader{repository: repository}
}

// ReadFinalTurns returns an all-or-nothing, account-scoped snapshot batch in caller order.
// The repository applies ownership filtering during its query; this method rejects incomplete or
// inconsistent result sets before they can become immutable outbound-message content.
func (r *FinalTurnReader) ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error) {
	if accountID == "" || len(turnIDs) == 0 || len(turnIDs) > recordsv1.MaxFinalTurnBatchSize {
		return nil, ErrInvalidRequest
	}

	requested := make(map[string]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		if turnID == "" {
			return nil, ErrInvalidRequest
		}
		if _, exists := requested[turnID]; exists {
			return nil, ErrInvalidRequest
		}
		requested[turnID] = struct{}{}
	}

	repositoryTurnIDs := append([]string(nil), turnIDs...)
	snapshots, err := r.repository.ReadFinalTurns(ctx, accountID, repositoryTurnIDs)
	if err != nil {
		return nil, err
	}

	byTurnID := make(map[string]recordsv1.FinalTurnSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.TurnID == "" {
			return nil, fmt.Errorf("read final turns: repository returned an empty turn ID")
		}
		if _, exists := requested[snapshot.TurnID]; !exists {
			return nil, fmt.Errorf("read final turns: repository returned unrequested turn %q", snapshot.TurnID)
		}
		if _, exists := byTurnID[snapshot.TurnID]; exists {
			return nil, fmt.Errorf("read final turns: repository returned duplicate turn %q", snapshot.TurnID)
		}
		byTurnID[snapshot.TurnID] = cloneFinalTurnSnapshot(snapshot)
	}

	ordered := make([]recordsv1.FinalTurnSnapshot, 0, len(turnIDs))
	for _, turnID := range turnIDs {
		snapshot, exists := byTurnID[turnID]
		if !exists {
			return nil, ErrTurnNotFound
		}
		ordered = append(ordered, snapshot)
	}
	return ordered, nil
}

var _ recordsv1.TurnReader = (*FinalTurnReader)(nil)

func cloneFinalTurnSnapshot(snapshot recordsv1.FinalTurnSnapshot) recordsv1.FinalTurnSnapshot {
	cloned := snapshot
	cloned.ParticipantID = cloneString(snapshot.ParticipantID)
	cloned.SpeakerLabelSnapshot = cloneString(snapshot.SpeakerLabelSnapshot)
	return cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
