package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
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
	wecomIdentity       WeComIdentityClient
	fallback            AutomaticTurnFallbackPlayer
	restorer            AutomaticTurnOutputRestorer
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

// ConfigureWeChatBinding wires the WeCom OAuth client used to resolve bind codes.
func (u *UseCases) ConfigureWeChatBinding(client WeComIdentityClient) {
	u.wecomIdentity = client
}

func (u *UseCases) ConfigureAutomaticFallback(player AutomaticTurnFallbackPlayer) {
	u.fallback = player
}

func (u *UseCases) ConfigureAutomaticOutputRestorer(restorer AutomaticTurnOutputRestorer) {
	u.restorer = restorer
}

// Create accepts selected final Turns and creates an asynchronous delivery task.
// It never calls SMTP or WeCom directly; success means the immutable Message,
// first Attempt, and queue handoff were accepted, not that delivery completed.
func (u *UseCases) Create(ctx context.Context, input CreateInput) (Message, error) {
	if u.repository == nil {
		return Message{}, domain.ErrNotImplemented
	}
	if input.AccountID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > MaxIdempotencyKeyLength || !IsSupportedChannel(input.Channel) || input.DestinationRef == "" || len(input.TurnIDs) == 0 || hasDuplicateTurnIDs(input.TurnIDs) {
		return Message{}, domain.ErrInvalidArgument
	}
	// Resolve idempotency before reading turns or destinations. The same account,
	// key, and business payload returns the original Message; a changed payload
	// must conflict instead of creating a second delivery.
	if existing, handled, err := u.resolveCreateIdempotency(ctx, input); handled || err != nil {
		return existing, err
	}
	if u.turns == nil || u.destinations == nil {
		return Message{}, domain.ErrInvalidArgument
	}
	// TurnReader checks account ownership and returns only persisted final records;
	// partial ASR results and client-supplied bodies cannot enter delivery.
	turns, err := u.turns.ReadFinalTurns(ctx, input.AccountID, input.TurnIDs)
	if err != nil {
		return Message{}, err
	}
	if len(turns) != len(input.TurnIDs) {
		return Message{}, domain.ErrNotFound
	}
	// Validate the account-scoped destination, but persist only its opaque
	// reference. Resolve and decrypt the real target immediately before provider
	// invocation; never copy it into Message snapshots or API responses.
	if _, err := u.destinations.ResolveVerifiedDestination(ctx, input.AccountID, input.Channel, input.DestinationRef); err != nil {
		return Message{}, err
	}
	now := time.Now().UTC()
	// Copy final Turns into a versioned immutable snapshot so retries keep the
	// creation-time content even if attribution metadata changes later.
	message := Message{ID: "msg_" + ulid.Make().String(), AccountID: input.AccountID, Channel: input.Channel, DestinationRef: input.DestinationRef, SnapshotVersion: 1, Turns: cloneTurns(turns), Status: MessageStatusQueued, Attempts: 1, CreatedAt: now, UpdatedAt: now}
	attempt := DeliveryAttempt{ID: "attempt_" + ulid.Make().String(), MessageID: message.ID, AttemptNumber: 1, Status: AttemptStatusQueued, CreatedAt: now}
	// The production repository writes Message, Attempt, and Outbox in one
	// transaction. This database-to-queue boundary leaves a durable task after
	// commit even if the API process crashes immediately afterward.
	if err := u.repository.CreateMessage(ctx, CreateMessageRecord{Message: message, InitialAttempt: attempt, IdempotencyKey: input.IdempotencyKey}); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			// Concurrent instances may both miss the lookup and race on insert. The
			// database unique constraint chooses the winner; reread an identical
			// request instead of returning a spurious failure.
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
	// The lightweight in-memory repository has no durable Outbox, so tests and
	// compatibility adapters enqueue directly. Production Outbox repositories
	// publish through OutboxDispatcher after commit.
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

// PutPreference changes one destination's automatic-delivery opt-in. Enabling
// always resolves the target first so a stale or revoked destination cannot be
// made ready for single-output mode.
func (u *UseCases) PutPreference(ctx context.Context, accountID string, channel Channel, destinationRef string, enabled bool) (Preference, error) {
	if u.repository == nil {
		return Preference{}, domain.ErrNotImplemented
	}
	destinationRef = strings.TrimSpace(destinationRef)
	if accountID == "" || !IsSupportedChannel(channel) || destinationRef == "" {
		return Preference{}, domain.ErrInvalidArgument
	}
	if enabled {
		if u.destinations == nil {
			return Preference{}, domain.ErrNotImplemented
		}
		if _, err := u.destinations.ResolveVerifiedDestination(ctx, accountID, channel, destinationRef); err != nil {
			return Preference{}, err
		}
	}
	// Verification is authoritative data owned by the destination store. The
	// repository derives it from the currently verified, non-revoked target;
	// the preference endpoint must not manufacture a verified state.
	return u.repository.PutPreference(ctx, Preference{AccountID: accountID, Channel: channel, DestinationRef: destinationRef, Enabled: enabled, UpdatedAt: time.Now().UTC()})
}

// ScheduleFinalTurn creates one independent message for each enabled target
// preference after the record-store consumer has committed the Final Turn.
// Replays are harmless because Create uses the stable per-turn idempotency key.
func (u *UseCases) ScheduleFinalTurn(ctx context.Context, accountID string, event recordsv1.FinalTurnEvent) error {
	if u == nil || u.repository == nil {
		return domain.ErrNotImplemented
	}
	if accountID == "" || event.TurnID == "" || !event.DeliveryEnabled {
		return nil
	}
	if scheduler, ok := u.repository.(AutomaticTurnSchedulerRepository); ok {
		return u.scheduleAutomaticTurnAtomically(ctx, scheduler, accountID, event)
	}
	preferences, err := u.Preferences(ctx, accountID)
	if err != nil {
		return err
	}
	for _, preference := range preferences {
		if !preference.Enabled || !preference.Verified || preference.DestinationRef == "" || !IsSupportedChannel(preference.Channel) {
			continue
		}
		key := fmt.Sprintf("auto:final_turn:%s:%s:%s", event.TurnID, preference.Channel, preference.DestinationRef)
		if _, err := u.Create(ctx, CreateInput{
			AccountID: accountID, IdempotencyKey: key, Channel: preference.Channel,
			DestinationRef: preference.DestinationRef, TurnIDs: []string{event.TurnID},
		}); err != nil {
			return fmt.Errorf("schedule final turn %s for %s: %w", event.TurnID, preference.Channel, err)
		}
	}
	return nil
}

func (u *UseCases) scheduleAutomaticTurnAtomically(ctx context.Context, scheduler AutomaticTurnSchedulerRepository, accountID string, event recordsv1.FinalTurnEvent) error {
	if event.SessionID == "" || event.TraceID == "" || event.TargetLanguage == "" || event.TranslatedText == "" || event.LanguageConfigVersion < 1 {
		return domain.ErrInvalidArgument
	}
	if u.turns == nil || u.destinations == nil {
		return domain.ErrInvalidArgument
	}
	existing, err := scheduler.GetAutomaticTurnRun(ctx, accountID, event.TurnID)
	if err == nil {
		if existing.SessionID != event.SessionID || existing.TraceID != event.TraceID || existing.TargetLanguage != event.TargetLanguage ||
			existing.TranslatedText != event.TranslatedText || existing.LanguageConfigVersion != event.LanguageConfigVersion {
			return domain.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	preferences, err := u.Preferences(ctx, accountID)
	if err != nil {
		return err
	}
	turns, err := u.turns.ReadFinalTurns(ctx, accountID, []string{event.TurnID})
	if err != nil {
		return err
	}
	if len(turns) != 1 {
		return domain.ErrNotFound
	}
	now := time.Now().UTC()
	targets := make([]AutomaticTargetRecord, 0, len(preferences))
	for _, preference := range preferences {
		if !preference.Enabled || !preference.Verified || preference.DestinationRef == "" || !IsSupportedChannel(preference.Channel) {
			continue
		}
		if _, err := u.destinations.ResolveVerifiedDestination(ctx, accountID, preference.Channel, preference.DestinationRef); err != nil {
			return err
		}
		message := Message{
			ID: "msg_" + ulid.Make().String(), AccountID: accountID, Channel: preference.Channel,
			DestinationRef: preference.DestinationRef, SnapshotVersion: 1, Turns: cloneTurns(turns),
			Status: MessageStatusQueued, Attempts: 1, CreatedAt: now, UpdatedAt: now,
		}
		attempt := DeliveryAttempt{ID: "attempt_" + ulid.Make().String(), MessageID: message.ID, AttemptNumber: 1, Status: AttemptStatusQueued, CreatedAt: now}
		key := fmt.Sprintf("auto:final_turn:%s:%s:%s", event.TurnID, preference.Channel, preference.DestinationRef)
		targets = append(targets, AutomaticTargetRecord{
			Message: message, InitialAttempt: attempt, IdempotencyKey: key,
			Settlement: AutomaticTurnSettlement{
				AccountID: accountID, TurnID: event.TurnID, SessionID: event.SessionID,
				TargetLanguage: event.TargetLanguage, Channel: preference.Channel,
				DestinationRef: preference.DestinationRef, Status: AutomaticTurnSettlementQueued,
				MessageID: message.ID, CreatedAt: now, UpdatedAt: now,
			},
		})
	}
	run := AutomaticTurnRun{
		AccountID: accountID, TurnID: event.TurnID, SessionID: event.SessionID, TraceID: event.TraceID,
		TargetLanguage: event.TargetLanguage, TranslatedText: event.TranslatedText,
		LanguageConfigVersion: event.LanguageConfigVersion, Status: AutomaticTurnRunPending,
		TargetCount: len(targets), FallbackOperationID: "fallback_" + event.TurnID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := scheduler.ScheduleAutomaticTurn(ctx, AutomaticTurnScheduleRecord{Run: run, Targets: targets}); err != nil {
		return fmt.Errorf("schedule automatic turn atomically: %w", err)
	}
	return nil
}

// RetryAutomaticTurnFailures queues the remaining failed targets for a Final Turn.
func (u *UseCases) RetryAutomaticTurnFailures(ctx context.Context, accountID, turnID string) error {
	retryRepository, ok := u.repository.(AutomaticTurnRetryRepository)
	if !ok {
		return domain.ErrNotImplemented
	}
	scheduler, ok := u.repository.(AutomaticTurnSchedulerRepository)
	if !ok {
		return domain.ErrNotImplemented
	}
	run, err := scheduler.GetAutomaticTurnRun(ctx, accountID, turnID)
	if err != nil {
		return err
	}
	if run.Status != AutomaticTurnRunPartiallySucceeded || run.SucceededCount == 0 || run.FailedCount == 0 {
		return nil
	}
	settlements, err := retryRepository.ListAutomaticTurnSettlements(ctx, accountID, turnID)
	if err != nil {
		return err
	}
	for _, settlement := range settlements {
		if settlement.Status != AutomaticTurnSettlementFailed || settlement.MessageID == "" {
			continue
		}
		message, err := u.Get(ctx, accountID, settlement.MessageID)
		if err != nil {
			return err
		}
		if message.Attempts >= maxAutomaticTargetAttempts {
			continue
		}
		key := fmt.Sprintf("auto:final_turn_retry:%s:%s:%s:%d", turnID, settlement.Channel, settlement.DestinationRef, message.Attempts+1)
		if _, err := retryRepository.RetryAutomaticTurnTarget(ctx, accountID, turnID, message.ID, key); err != nil {
			return fmt.Errorf("retry automatic target %s: %w", settlement.DestinationRef, err)
		}
	}
	return nil
}

// RetryAutomaticTurns processes a bounded batch of automatic retry candidates.
func (u *UseCases) RetryAutomaticTurns(ctx context.Context, limit int) error {
	repository, ok := u.repository.(AutomaticTurnRetryRepository)
	if !ok {
		return domain.ErrNotImplemented
	}
	if limit <= 0 {
		limit = 20
	}
	candidates, err := repository.ListAutomaticTurnRetryCandidates(ctx, limit)
	if err != nil {
		return err
	}
	for _, run := range candidates {
		if err := u.RetryAutomaticTurnFailures(ctx, run.AccountID, run.TurnID); err != nil {
			return err
		}
	}
	return nil
}

// RecoverAutomaticTurn plays and durably records one automatic fallback snapshot.
func (u *UseCases) RecoverAutomaticTurn(ctx context.Context, accountID, turnID string) error {
	repository, ok := u.repository.(AutomaticTurnFallbackRepository)
	if !ok || u.fallback == nil {
		return domain.ErrNotImplemented
	}
	run, claimed, err := repository.ClaimAutomaticTurnFallback(ctx, accountID, turnID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	_, err = u.fallback.PlayFallback(ctx, run.SessionID, realtimev1.FallbackPlaybackRequest{
		OperationID: run.FallbackOperationID, SessionID: run.SessionID, TurnID: run.TurnID,
		TargetLanguage: run.TargetLanguage, TranslatedText: run.TranslatedText,
		LanguageConfigVersion: int(run.LanguageConfigVersion), TraceID: run.TraceID,
	})
	if err != nil {
		return fmt.Errorf("play automatic fallback: %w", err)
	}
	if err := repository.MarkAutomaticTurnFallbackPlayed(ctx, accountID, turnID); err != nil {
		return fmt.Errorf("mark automatic fallback played: %w", err)
	}
	return nil
}

// RecoverAutomaticTurns processes a bounded batch of fallback playback candidates.
func (u *UseCases) RecoverAutomaticTurns(ctx context.Context, limit int) error {
	repository, ok := u.repository.(AutomaticTurnFallbackRepository)
	if !ok || u.fallback == nil {
		return domain.ErrNotImplemented
	}
	if limit <= 0 {
		limit = 20
	}
	candidates, err := repository.ListAutomaticTurnRecoveryCandidates(ctx, limit)
	if err != nil {
		return err
	}
	for _, run := range candidates {
		if err := u.RecoverAutomaticTurn(ctx, run.AccountID, run.TurnID); err != nil {
			return err
		}
	}
	return nil
}

// RestoreAutomaticTurn restores bidirectional output for one completed fallback.
func (u *UseCases) RestoreAutomaticTurn(ctx context.Context, accountID, turnID string) error {
	repository, ok := u.repository.(AutomaticTurnFallbackRepository)
	if !ok || u.restorer == nil {
		return domain.ErrNotImplemented
	}
	scheduler, ok := u.repository.(AutomaticTurnSchedulerRepository)
	if !ok {
		return domain.ErrNotImplemented
	}
	run, err := scheduler.GetAutomaticTurnRun(ctx, accountID, turnID)
	if err != nil {
		return err
	}
	if run.Status != AutomaticTurnRunFallbackPlayed {
		return domain.ErrConflict
	}
	if err := u.restorer.RestoreBidirectionalOutput(ctx, run.AccountID, run.SessionID, int(run.LanguageConfigVersion), "restore_"+run.FallbackOperationID); err != nil {
		return fmt.Errorf("restore bidirectional output: %w", err)
	}
	return repository.MarkAutomaticTurnRestored(ctx, accountID, turnID)
}

// RestoreAutomaticTurns processes a bounded batch of output-restore candidates.
func (u *UseCases) RestoreAutomaticTurns(ctx context.Context, limit int) error {
	repository, ok := u.repository.(AutomaticTurnFallbackRepository)
	if !ok || u.restorer == nil {
		return domain.ErrNotImplemented
	}
	if limit <= 0 {
		limit = 20
	}
	candidates, err := repository.ListAutomaticTurnRestoreCandidates(ctx, limit)
	if err != nil {
		return err
	}
	for _, run := range candidates {
		if err := u.RestoreAutomaticTurn(ctx, run.AccountID, run.TurnID); err != nil {
			return err
		}
	}
	return nil
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

func (u *UseCases) BindEmailTarget(ctx context.Context, accountID, token string) (target MessageTarget, err error) {
	repository := targetRepository(u.repository)
	if repository == nil || accountID == "" || len(u.destinationKey) != 32 {
		return MessageTarget{}, domain.ErrNotImplemented
	}
	resolved, err := resolveEmailBindToken(ctx, u.appEnv, token, accountID, u.emailBindChallenges)
	if err != nil {
		return MessageTarget{}, err
	}
	completed := false
	if resolved.ChallengeID != "" {
		defer func() {
			if completed {
				return
			}
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), emailBindChallengeRestoreTimeout)
			defer cancel()
			if restoreErr := u.emailBindChallenges.RestoreEmailBindChallenge(restoreCtx, resolved.ChallengeID); restoreErr != nil {
				err = fmt.Errorf("bind email target: %w", errors.Join(err, fmt.Errorf("restore consumed challenge: %w", restoreErr)))
			}
		}()
	}
	ciphertext, err := EncryptProviderTarget(u.destinationKey, resolved.Email)
	if err != nil {
		return MessageTarget{}, err
	}
	now := time.Now().UTC()
	target, err = repository.BindEmailTarget(ctx, BindEmailTargetRecord{
		ID:             "dest_" + ulid.Make().String(),
		AccountID:      accountID,
		DestinationRef: resolved.DestinationRef,
		Ciphertext:     ciphertext,
		KeyVersion:     destinationKeyVersion,
		VerifiedAt:     now,
	})
	if err != nil {
		return MessageTarget{}, err
	}
	completed = true
	return target, nil
}

func (u *UseCases) BindWeChatTarget(ctx context.Context, accountID, code string) (MessageTarget, error) {
	repository := targetRepository(u.repository)
	if repository == nil || accountID == "" || len(u.destinationKey) != 32 {
		return MessageTarget{}, domain.ErrNotImplemented
	}
	destinationRef, userid, err := resolveWeChatBindCode(ctx, u.appEnv, code, u.wecomIdentity)
	if err != nil {
		return MessageTarget{}, err
	}
	ciphertext, err := EncryptProviderTarget(u.destinationKey, userid)
	if err != nil {
		return MessageTarget{}, err
	}
	now := time.Now().UTC()
	return repository.BindWeChatTarget(ctx, BindWeChatTargetRecord{
		ID:             "dest_" + ulid.Make().String(),
		AccountID:      accountID,
		DestinationRef: destinationRef,
		Ciphertext:     ciphertext,
		KeyVersion:     destinationKeyVersion,
		VerifiedAt:     now,
	})
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
