package turns

import (
	"context"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// ReadFinalTurns returns an all-or-nothing, account-scoped snapshot batch in caller order.
// The repository applies ownership filtering during its query; this method rejects incomplete or
// inconsistent result sets before they can become immutable outbound-message content.
func (s *Service) ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error) {
	if accountID == "" || len(turnIDs) == 0 || len(turnIDs) > recordsv1.MaxFinalTurnBatchSize {
		return nil, ErrInvalidRequest
	}

	requestedOrder := append([]string(nil), turnIDs...)
	requested := make(map[string]struct{}, len(requestedOrder))
	for _, turnID := range requestedOrder {
		if turnID == "" {
			return nil, ErrInvalidRequest
		}
		if _, exists := requested[turnID]; exists {
			return nil, ErrInvalidRequest
		}
		requested[turnID] = struct{}{}
	}

	repositoryTurnIDs := append([]string(nil), requestedOrder...)
	snapshots, err := s.repository.ReadFinalTurns(ctx, accountID, repositoryTurnIDs)
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

	ordered := make([]recordsv1.FinalTurnSnapshot, 0, len(requestedOrder))
	for _, turnID := range requestedOrder {
		snapshot, exists := byTurnID[turnID]
		if !exists {
			return nil, ErrTurnNotFound
		}
		ordered = append(ordered, snapshot)
	}
	return ordered, nil
}

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
