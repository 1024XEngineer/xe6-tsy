package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/oklog/ulid/v2"
)

type UseCases struct {
	repository          Repository
	turns               TurnReader
	destinations        DestinationReader
	queue               Queue
	keys                sync.Mutex
	createKeys          map[string]string
	retryKeys           map[string]string
	destinationKey      []byte
	appEnv              string
	emailBindChallenges EmailBindChallengeRepository
	emailBindSender     EmailBindSender
}

func NewUseCases() *UseCases { return &UseCases{} }

func NewPersistentUseCases(repository Repository, turns TurnReader, destinations DestinationReader, queue Queue) *UseCases {
	return &UseCases{repository: repository, turns: turns, destinations: destinations, queue: queue, createKeys: make(map[string]string), retryKeys: make(map[string]string)}
}

// ConfigureTargetBinding enables message-target bind/list/unbind operations on a
// persistent deployment. Without destination key material the routes fail closed.
func (u *UseCases) ConfigureTargetBinding(destinationKey []byte, appEnv string) {
	if len(destinationKey) == 32 {
		u.destinationKey = append([]byte(nil), destinationKey...)
	}
	u.appEnv = appEnv
}

// ConfigureEmailVerification wires durable bind challenges and outbound token delivery.
func (u *UseCases) ConfigureEmailVerification(challenges EmailBindChallengeRepository, sender EmailBindSender) {
	u.emailBindChallenges = challenges
	u.emailBindSender = sender
}

func (u *UseCases) Create(ctx context.Context, input CreateInput) (Message, error) {
	if u.repository == nil {
		return Message{}, domain.ErrNotImplemented
	}
	if input.AccountID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > MaxIdempotencyKeyLength || !IsSupportedChannel(input.Channel) || input.DestinationRef == "" || len(input.TurnIDs) == 0 || hasDuplicateTurnIDs(input.TurnIDs) {
		return Message{}, domain.ErrInvalidArgument
	}
	if existing, handled, err := u.resolveCreateIdempotency(ctx, input); handled || err != nil {
		return existing, err
	}
	if u.turns == nil || u.destinations == nil {
		return Message{}, domain.ErrInvalidArgument
	}
	turns, err := u.turns.ReadFinalTurns(ctx, input.AccountID, input.TurnIDs)
	if err != nil {
		return Message{}, err
	}
	if len(turns) != len(input.TurnIDs) {
		return Message{}, domain.ErrNotFound
	}
	if _, err := u.destinations.ResolveVerifiedDestination(ctx, input.AccountID, input.Channel, input.DestinationRef); err != nil {
		return Message{}, err
	}
	now := time.Now().UTC()
	message := Message{ID: "msg_" + ulid.Make().String(), AccountID: input.AccountID, Channel: input.Channel, DestinationRef: input.DestinationRef, SnapshotVersion: 1, Turns: cloneTurns(turns), Status: MessageStatusQueued, Attempts: 1, CreatedAt: now, UpdatedAt: now}
	attempt := DeliveryAttempt{ID: "attempt_" + ulid.Make().String(), MessageID: message.ID, AttemptNumber: 1, Status: AttemptStatusQueued, CreatedAt: now}
	if err := u.repository.CreateMessage(ctx, CreateMessageRecord{Message: message, InitialAttempt: attempt, IdempotencyKey: input.IdempotencyKey}); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			if existing, handled, lookupErr := u.resolveCreateIdempotency(ctx, input); handled || lookupErr != nil {
				return existing, lookupErr
			}
		}
		return Message{}, err
	}
	u.keys.Lock()
	if u.createKeys == nil {
		u.createKeys = make(map[string]string)
	}
	u.createKeys[scopedIdempotencyKey(input.AccountID, input.IdempotencyKey)] = message.ID
	u.keys.Unlock()
	if !isOutboxBacked(u.repository) && u.queue != nil {
		if err := u.queue.Enqueue(ctx, attempt.ID, input.IdempotencyKey); err != nil {
			return Message{}, err
		}
	}
	return message, nil
}

func (u *UseCases) resolveCreateIdempotency(ctx context.Context, input CreateInput) (Message, bool, error) {
	reader, ok := u.repository.(IdempotencyReader)
	if !ok {
		return Message{}, false, nil
	}
	existing, err := reader.GetMessageByIdempotency(ctx, input.AccountID, input.IdempotencyKey)
	if errors.Is(err, domain.ErrNotFound) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, true, err
	}
	if existing.AccountID != input.AccountID || existing.Channel != input.Channel || existing.DestinationRef != input.DestinationRef || !sameTurnSelection(existing.Turns, input.TurnIDs) {
		return Message{}, true, domain.ErrConflict
	}
	return existing, true, nil
}

func (u *UseCases) Get(ctx context.Context, accountID, messageID string) (Message, error) {
	if u.repository == nil {
		return Message{}, domain.ErrNotImplemented
	}
	return u.repository.GetMessage(ctx, accountID, messageID)
}

func (u *UseCases) Retry(ctx context.Context, accountID, messageID, key string) (Message, error) {
	if u.repository == nil {
		return Message{}, domain.ErrNotImplemented
	}
	if accountID == "" || messageID == "" || key == "" || len(key) > MaxIdempotencyKeyLength {
		return Message{}, domain.ErrInvalidArgument
	}
	u.keys.Lock()
	known := u.retryKeys[scopedIdempotencyKey(accountID, key)]
	u.keys.Unlock()
	if known != "" {
		if known != messageID {
			return Message{}, domain.ErrConflict
		}
		return u.repository.GetMessage(ctx, accountID, messageID)
	}
	if existing, handled, err := u.resolveRetryIdempotency(ctx, accountID, messageID, key); handled || err != nil {
		return existing, err
	}
	current, err := u.repository.GetMessage(ctx, accountID, messageID)
	if err != nil {
		return Message{}, err
	}
	if current.Status != MessageStatusFailed || (current.LastErrorCode != nil && *current.LastErrorCode == deliveryUnknownErrorCode) {
		return Message{}, domain.ErrConflict
	}
	now := time.Now().UTC()
	attempt := DeliveryAttempt{ID: "attempt_" + ulid.Make().String(), MessageID: messageID, AttemptNumber: current.Attempts + 1, Status: AttemptStatusQueued, CreatedAt: now}
	message, err := u.repository.CreateRetry(ctx, CreateRetryRecord{AccountID: accountID, MessageID: messageID, Attempt: attempt, IdempotencyKey: key})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			if existing, handled, lookupErr := u.resolveRetryIdempotency(ctx, accountID, messageID, key); handled || lookupErr != nil {
				return existing, lookupErr
			}
		}
		return Message{}, err
	}
	u.keys.Lock()
	if u.retryKeys == nil {
		u.retryKeys = make(map[string]string)
	}
	u.retryKeys[scopedIdempotencyKey(accountID, key)] = messageID
	u.keys.Unlock()
	if !isOutboxBacked(u.repository) && u.queue != nil {
		if err := u.queue.Enqueue(ctx, attempt.ID, key); err != nil {
			return Message{}, err
		}
	}
	return message, nil
}

func (u *UseCases) resolveRetryIdempotency(ctx context.Context, accountID, messageID, key string) (Message, bool, error) {
	var (
		existing  Message
		lookupErr = domain.ErrNotFound
	)
	switch reader := u.repository.(type) {
	case RetryIdempotencyReader:
		existing, lookupErr = reader.GetMessageByDeliveryIdempotency(ctx, accountID, key)
	case IdempotencyReader:
		// Compatibility fallback for memory adapters that predate the durable
		// outbox-specific lookup boundary.
		existing, lookupErr = reader.GetMessageByIdempotency(ctx, accountID, key)
	default:
		return Message{}, false, nil
	}
	if errors.Is(lookupErr, domain.ErrNotFound) {
		return Message{}, false, nil
	}
	if lookupErr != nil {
		return Message{}, true, lookupErr
	}
	if existing.ID != messageID {
		return Message{}, true, domain.ErrConflict
	}
	return existing, true, nil
}

func (u *UseCases) Preferences(ctx context.Context, accountID string) ([]Preference, error) {
	if u.repository == nil {
		return nil, domain.ErrNotImplemented
	}
	return u.repository.ListPreferences(ctx, accountID)
}

func (u *UseCases) PutPreference(ctx context.Context, accountID string, channel Channel, enabled bool) (Preference, error) {
	if u.repository == nil {
		return Preference{}, domain.ErrNotImplemented
	}
	if accountID == "" || !IsSupportedChannel(channel) {
		return Preference{}, domain.ErrInvalidArgument
	}
	// Verification is authoritative data owned by the destination store. The
	// repository derives it from the currently verified, non-revoked target;
	// the preference endpoint must not manufacture a verified state.
	return u.repository.PutPreference(ctx, Preference{AccountID: accountID, Channel: channel, Enabled: enabled, UpdatedAt: time.Now().UTC()})
}

func (u *UseCases) ListMessageTargets(ctx context.Context, accountID string, channel *Channel) ([]MessageTarget, error) {
	repository := targetRepository(u.repository)
	if repository == nil || accountID == "" {
		return nil, domain.ErrNotImplemented
	}
	return repository.ListMessageTargets(ctx, accountID, channel)
}

func (u *UseCases) RequestEmailBindVerification(ctx context.Context, accountID, email, destinationRef string) error {
	if u.emailBindChallenges == nil || u.emailBindSender == nil || accountID == "" || len(u.destinationKey) != 32 {
		return domain.ErrNotImplemented
	}
	normalizedEmail, err := normalizeBindEmail(email)
	if err != nil {
		return err
	}
	destinationRef = normalizeBindDestinationRef(destinationRef)
	token, err := generateEmailBindToken()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	challenge := newEmailBindChallenge(accountID, destinationRef, normalizedEmail, hashEmailBindToken(token), now)
	if err := u.emailBindChallenges.CreateEmailBindChallenge(ctx, challenge); err != nil {
		return err
	}
	return u.emailBindSender.SendBindToken(ctx, normalizedEmail, destinationRef, token)
}

func (u *UseCases) BindEmailTarget(ctx context.Context, accountID, token string) (MessageTarget, error) {
	repository := targetRepository(u.repository)
	if repository == nil || accountID == "" || len(u.destinationKey) != 32 {
		return MessageTarget{}, domain.ErrNotImplemented
	}
	destinationRef, email, err := resolveEmailBindToken(ctx, u.appEnv, token, accountID, u.emailBindChallenges)
	if err != nil {
		return MessageTarget{}, err
	}
	ciphertext, err := EncryptProviderTarget(u.destinationKey, email)
	if err != nil {
		return MessageTarget{}, err
	}
	now := time.Now().UTC()
	return repository.BindEmailTarget(ctx, BindEmailTargetRecord{
		ID:             "dest_" + ulid.Make().String(),
		AccountID:      accountID,
		DestinationRef: destinationRef,
		Ciphertext:     ciphertext,
		KeyVersion:     destinationKeyVersion,
		VerifiedAt:     now,
	})
}

func (u *UseCases) BindWeChatTarget(context.Context, string, string) (MessageTarget, error) {
	return MessageTarget{}, domain.ErrNotImplemented
}

func (u *UseCases) RevokeMessageTarget(ctx context.Context, accountID string, channel Channel, destinationRef string) error {
	repository := targetRepository(u.repository)
	if repository == nil || accountID == "" {
		return domain.ErrNotImplemented
	}
	return repository.RevokeMessageTarget(ctx, accountID, channel, destinationRef, time.Now().UTC())
}

func isOutboxBacked(repository Repository) bool { _, ok := repository.(OutboxRepository); return ok }

func scopedIdempotencyKey(accountID, key string) string {
	return accountID + "\x00" + key
}

func hasDuplicateTurnIDs(turnIDs []string) bool {
	seen := make(map[string]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		if turnID == "" {
			return true
		}
		if _, exists := seen[turnID]; exists {
			return true
		}
		seen[turnID] = struct{}{}
	}
	return false
}

func sameTurnSelection(turns []FinalTurnSnapshot, turnIDs []string) bool {
	if len(turns) != len(turnIDs) {
		return false
	}
	want := make(map[string]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		if turnID == "" {
			return false
		}
		want[turnID] = struct{}{}
	}
	if len(want) != len(turnIDs) {
		return false
	}
	for _, turn := range turns {
		if _, exists := want[turn.TurnID]; !exists {
			return false
		}
		delete(want, turn.TurnID)
	}
	return len(want) == 0
}

func cloneTurns(source []FinalTurnSnapshot) []FinalTurnSnapshot {
	result := make([]FinalTurnSnapshot, len(source))
	for index, turn := range source {
		result[index] = turn
		result[index].ParticipantID = cloneString(turn.ParticipantID)
		result[index].SpeakerLabelSnapshot = cloneString(turn.SpeakerLabelSnapshot)
	}
	return result
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
