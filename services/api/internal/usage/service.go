package usage

import (
	"context"
	"math/big"
	"regexp"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type UseCases struct {
	repository Repository
	owners     SessionOwnerReader
}

// These patterns mirror the usage.recorded v1 contract. An empty value means
// that the provider did not report pricing; it is distinct from a reported
// zero amount and is stored as SQL NULL by the PostgreSQL adapter.
var (
	usageCostPattern     = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
	usageCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

func NewUseCases() *UseCases { return &UseCases{} }

func NewPersistentUseCases(repository Repository, owners SessionOwnerReader) *UseCases {
	return &UseCases{repository: repository, owners: owners}
}

func (u *UseCases) Record(ctx context.Context, input RecordInput) (Detail, error) {
	if u.repository == nil {
		return Detail{}, domain.ErrNotImplemented
	}
	if err := validate(input); err != nil {
		return Detail{}, err
	}
	if u.owners != nil {
		owner, err := u.owners.AccountIDForSession(ctx, input.SessionID)
		if err != nil {
			return Detail{}, err
		}
		if err := u.sameCanonicalAccount(ctx, owner, input.AccountID); err != nil {
			return Detail{}, err
		}
		input.AccountID = owner
	}
	detail, _, err := u.repository.Record(ctx, input)
	return detail, err
}

func (u *UseCases) sameCanonicalAccount(ctx context.Context, left, right string) error {
	if left == right {
		return nil
	}
	resolver, ok := u.owners.(CanonicalAccountResolver)
	if !ok {
		return domain.ErrForbidden
	}
	canonicalLeft, err := resolver.CanonicalAccountID(ctx, left)
	if err != nil {
		return err
	}
	canonicalRight, err := resolver.CanonicalAccountID(ctx, right)
	if err != nil {
		return err
	}
	if canonicalLeft != canonicalRight {
		return domain.ErrForbidden
	}
	return nil
}

func (u *UseCases) SessionUsage(ctx context.Context, accountID, sessionID string) (Summary, error) {
	if u.repository == nil {
		return Summary{}, domain.ErrNotImplemented
	}
	if accountID == "" || sessionID == "" {
		return Summary{}, domain.ErrInvalidArgument
	}
	if u.owners != nil {
		owner, err := u.owners.AccountIDForSession(ctx, sessionID)
		if err != nil {
			return Summary{}, err
		}
		if owner != accountID {
			return Summary{}, domain.ErrForbidden
		}
	}
	return u.repository.SessionSummary(ctx, accountID, sessionID)
}

func (u *UseCases) AccountUsage(ctx context.Context, accountID string, start, end time.Time) (Summary, error) {
	if u.repository == nil {
		return Summary{}, domain.ErrNotImplemented
	}
	if accountID == "" || start.IsZero() || end.IsZero() || !start.Before(end) {
		return Summary{}, domain.ErrInvalidArgument
	}
	return u.repository.AccountSummary(ctx, accountID, start, end)
}

func validate(input RecordInput) error {
	if input.EventVersion != UsageEventVersion || input.ID == "" || input.TraceID == "" || input.IdempotencyKey == "" || input.AccountID == "" || input.SessionID == "" || input.TurnID == "" || input.Provider == "" || input.Model == "" || input.OccurredAt.IsZero() {
		return domain.ErrInvalidArgument
	}
	switch input.ServiceType {
	case StageASR, StageTranslation, StageTTS, StageDiarization:
	default:
		return domain.ErrInvalidArgument
	}
	if input.InputTokens < 0 || input.OutputTokens < 0 || input.AudioDurationMS < 0 {
		return domain.ErrInvalidArgument
	}
	if input.CostAmount != "" {
		if !usageCostPattern.MatchString(input.CostAmount) {
			return domain.ErrInvalidArgument
		}
		if _, ok := new(big.Rat).SetString(input.CostAmount); !ok {
			return domain.ErrInvalidArgument
		}
	}
	if input.Currency != "" && !usageCurrencyPattern.MatchString(input.Currency) {
		return domain.ErrInvalidArgument
	}
	return nil
}

// MemoryRepository remains available for deterministic local tests; production
// wiring uses PostgresRepository below.
type MemoryRepository struct {
	mu    sync.RWMutex
	facts []Detail
	byKey map[string]Detail
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{byKey: make(map[string]Detail)}
}
func (r *MemoryRepository) Record(ctx context.Context, input RecordInput) (Detail, bool, error) {
	if err := ctx.Err(); err != nil {
		return Detail{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.byKey[input.IdempotencyKey]; ok {
		return old, false, nil
	}
	detail := Detail{RecordInput: input, RecordedAt: time.Now().UTC()}
	r.byKey[input.IdempotencyKey] = detail
	r.facts = append(r.facts, detail)
	return detail, true, nil
}
func (r *MemoryRepository) SessionSummary(ctx context.Context, accountID, sessionID string) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return summarize(r.facts, accountID, sessionID, time.Time{}, time.Time{})
}
func (r *MemoryRepository) AccountSummary(ctx context.Context, accountID string, start, end time.Time) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return summarize(r.facts, accountID, "", start, end)
}
func summarize(facts []Detail, accountID, sessionID string, start, end time.Time) (Summary, error) {
	totals := map[Stage]*StageTotal{}
	for _, fact := range facts {
		if fact.AccountID != accountID || (sessionID != "" && fact.SessionID != sessionID) || (!start.IsZero() && (fact.OccurredAt.Before(start) || !fact.OccurredAt.Before(end))) {
			continue
		}
		total := totals[fact.ServiceType]
		if total == nil {
			total = &StageTotal{ServiceType: fact.ServiceType, Currency: fact.Currency}
			totals[fact.ServiceType] = total
		} else if total.Currency != fact.Currency {
			// A single stage total cannot safely combine different currencies;
			// this matches the PostgreSQL adapter's grouped-query conflict rule.
			return Summary{}, domain.ErrConflict
		}
		total.InputTokens += fact.InputTokens
		total.OutputTokens += fact.OutputTokens
		total.AudioDurationMS += fact.AudioDurationMS
		if fact.CostAmount != "" {
			total.CostAmount = addMoney(total.CostAmount, fact.CostAmount)
		}
	}
	result := Summary{AccountID: accountID, SessionID: sessionID, PeriodStart: start, PeriodEnd: end, Totals: make([]StageTotal, 0)}
	for _, stage := range []Stage{StageASR, StageTranslation, StageTTS, StageDiarization} {
		if total := totals[stage]; total != nil {
			result.Totals = append(result.Totals, *total)
		}
	}
	return result, nil
}
func addMoney(left, right string) string {
	if left == "" && right == "" {
		return ""
	}
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	l, _ := new(big.Rat).SetString(left)
	r, _ := new(big.Rat).SetString(right)
	l.Add(l, r)
	return l.FloatString(6)
}
