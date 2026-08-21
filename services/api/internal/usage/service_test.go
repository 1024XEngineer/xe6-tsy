package usage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func validRecordInput() RecordInput {
	return RecordInput{
		EventVersion:   UsageEventVersion,
		ID:             "usage-1",
		TraceID:        "trace-1",
		IdempotencyKey: "usage-key-1",
		AccountID:      "account-1",
		SessionID:      "session-1",
		TurnID:         "turn-1",
		ServiceType:    StageTranslation,
		Provider:       "provider-1",
		Model:          "model-1",
		OccurredAt:     time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
}

func TestValidateAllowsCompleteOrUnavailablePricing(t *testing.T) {
	for name, input := range map[string]RecordInput{
		"both missing": validRecordInput(),
		"both present": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "0.25"
			input.Currency = "CNY"
			return input
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(input); err != nil {
				t.Fatalf("validate() error = %v", err)
			}
		})
	}
}

func TestValidateAllowsAssistantLLMStage(t *testing.T) {
	input := validRecordInput()
	input.ServiceType = StageAssistantLLM
	if err := validate(input); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestValidateRejectsIncompletePricing(t *testing.T) {
	for name, input := range map[string]RecordInput{
		"cost only": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "0.25"
			return input
		}(),
		"currency only": func() RecordInput {
			input := validRecordInput()
			input.Currency = "CNY"
			return input
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(input); err != domain.ErrInvalidArgument {
				t.Fatalf("validate() error = %v, want %v", err, domain.ErrInvalidArgument)
			}
		})
	}
}

func TestValidateEnforcesIdempotencyKeyContractLimit(t *testing.T) {
	input := validRecordInput()
	input.IdempotencyKey = strings.Repeat("a", maxIdempotencyKeyLength)
	if err := validate(input); err != nil {
		t.Fatalf("validate() exact limit error = %v", err)
	}

	input.IdempotencyKey += "a"
	if err := validate(input); err != domain.ErrInvalidArgument {
		t.Fatalf("validate() over limit error = %v, want %v", err, domain.ErrInvalidArgument)
	}
}

func TestValidateRejectsCostsPostgresCannotRepresentExactly(t *testing.T) {
	for name, amount := range map[string]string{
		"more than eight fractional digits": "0.000000001",
		"more than twelve integer digits":   "1234567890123",
	} {
		t.Run(name, func(t *testing.T) {
			input := validRecordInput()
			input.CostAmount = amount
			input.Currency = "CNY"
			if err := validate(input); err != domain.ErrInvalidArgument {
				t.Fatalf("validate() error = %v, want %v", err, domain.ErrInvalidArgument)
			}
		})
	}
}

func TestValidateRejectsMalformedPricing(t *testing.T) {
	for name, input := range map[string]RecordInput{
		"negative cost": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "-1"
			input.Currency = "CNY"
			return input
		}(),
		"leading zero": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "00.1"
			input.Currency = "CNY"
			return input
		}(),
		"exponent": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "1e-3"
			input.Currency = "CNY"
			return input
		}(),
		"lowercase currency": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "1"
			input.Currency = "cny"
			return input
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(input); err == nil {
				t.Fatal("validate() succeeded for malformed pricing")
			}
		})
	}
}

func TestMemorySummaryHandlesUnknownCost(t *testing.T) {
	repository := NewMemoryRepository()
	input := validRecordInput()
	if _, _, err := repository.Record(context.Background(), input); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	summary, err := repository.AccountSummary(context.Background(), input.AccountID, input.OccurredAt.Add(-time.Hour), input.OccurredAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("AccountSummary() error = %v", err)
	}
	if len(summary.Totals) != 1 {
		t.Fatalf("len(summary.Totals) = %d, want 1", len(summary.Totals))
	}
	if got := summary.Totals[0].CostAmount; got != "" {
		t.Fatalf("CostAmount = %q, want empty unknown value", got)
	}
	if got := summary.Totals[0].Currency; got != "" {
		t.Fatalf("Currency = %q, want empty", got)
	}
}

func TestMemoryRepositoryRejectsIdempotencyKeyPayloadConflict(t *testing.T) {
	repository := NewMemoryRepository()
	input := validRecordInput()
	first, created, err := repository.Record(t.Context(), input)
	if err != nil {
		t.Fatalf("first Record() error = %v", err)
	}
	if !created {
		t.Fatal("first Record() created = false, want true")
	}

	replayed, created, err := repository.Record(t.Context(), input)
	if err != nil {
		t.Fatalf("replayed Record() error = %v", err)
	}
	if created {
		t.Fatal("replayed Record() created = true, want false")
	}
	if replayed != first {
		t.Fatalf("replayed Record() = %#v, want %#v", replayed, first)
	}

	conflicting := input
	conflicting.ID = "usage-conflict"
	if _, _, err := repository.Record(t.Context(), conflicting); err != domain.ErrConflict {
		t.Fatalf("conflicting Record() error = %v, want %v", err, domain.ErrConflict)
	}
}

func TestMemorySummaryUsesPostgresCostScale(t *testing.T) {
	repository := NewMemoryRepository()
	first := validRecordInput()
	first.CostAmount, first.Currency = "0.00000001", "CNY"
	second := first
	second.ID = "usage-2"
	second.IdempotencyKey = "usage-key-2"
	second.CostAmount = "0.00000002"
	for _, input := range []RecordInput{first, second} {
		if _, _, err := repository.Record(t.Context(), input); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	summary, err := repository.AccountSummary(t.Context(), first.AccountID, first.OccurredAt.Add(-time.Hour), first.OccurredAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("AccountSummary() error = %v", err)
	}
	if got := summary.Totals[0].CostAmount; got != "0.00000003" {
		t.Fatalf("CostAmount = %q, want %q", got, "0.00000003")
	}
}

func TestMemorySummaryRejectsPartiallyMissingCost(t *testing.T) {
	repository := NewMemoryRepository()
	first := validRecordInput()
	first.CostAmount, first.Currency = "0.25", "CNY"
	second := first
	second.ID = "usage-2"
	second.IdempotencyKey = "usage-key-2"
	second.CostAmount = ""
	for _, input := range []RecordInput{first, second} {
		if _, _, err := repository.Record(t.Context(), input); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	if _, err := repository.AccountSummary(t.Context(), first.AccountID, first.OccurredAt.Add(-time.Hour), first.OccurredAt.Add(time.Hour)); err != domain.ErrConflict {
		t.Fatalf("AccountSummary() error = %v, want %v", err, domain.ErrConflict)
	}
}

func TestAggregateCostRejectsIncompletePricing(t *testing.T) {
	for name, test := range map[string]struct {
		amount    string
		currency  string
		rowCount  int64
		costCount int64
	}{
		"some rows missing cost": {amount: "0.25000000", currency: "CNY", rowCount: 2, costCount: 1},
		"currency without cost":  {amount: "0", currency: "CNY", rowCount: 1, costCount: 0},
		"cost without currency":  {amount: "0.25000000", rowCount: 1, costCount: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := aggregateCost(test.amount, test.currency, test.rowCount, test.costCount); err != domain.ErrConflict {
				t.Fatalf("aggregateCost() error = %v, want %v", err, domain.ErrConflict)
			}
		})
	}
}

func TestAggregateCostNormalizesPostgresAmount(t *testing.T) {
	amount, err := aggregateCost("0.3", "CNY", 2, 2)
	if err != nil {
		t.Fatalf("aggregateCost() error = %v", err)
	}
	if amount != "0.30000000" {
		t.Fatalf("aggregateCost() = %q, want %q", amount, "0.30000000")
	}
}

func TestMemorySummaryReturnsEmptyArray(t *testing.T) {
	repository := NewMemoryRepository()

	summary, err := repository.AccountSummary(t.Context(), "acct-empty", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("AccountSummary() error = %v", err)
	}
	if summary.Totals == nil || len(summary.Totals) != 0 {
		t.Fatalf("AccountSummary().Totals = %#v, want non-nil empty slice", summary.Totals)
	}
}

func TestMemorySummaryRejectsMixedCurrencies(t *testing.T) {
	repository := NewMemoryRepository()
	first := validRecordInput()
	first.CostAmount, first.Currency = "1", "CNY"
	second := first
	second.ID = "usage-2"
	second.IdempotencyKey = "usage-key-2"
	second.CostAmount, second.Currency = "1", "USD"
	for _, input := range []RecordInput{first, second} {
		if _, _, err := repository.Record(context.Background(), input); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	if _, err := repository.AccountSummary(context.Background(), first.AccountID, first.OccurredAt.Add(-time.Hour), first.OccurredAt.Add(time.Hour)); err != domain.ErrConflict {
		t.Fatalf("AccountSummary() error = %v, want %v", err, domain.ErrConflict)
	}
}

func TestRecordStoresOriginalSessionOwnerAfterAccountMerge(t *testing.T) {
	repository := &captureRepository{}
	input := validRecordInput()
	input.AccountID = "account-registered"

	detail, err := NewPersistentUseCases(repository, mergedSessionOwner{}).Record(t.Context(), input)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if repository.input.AccountID != "account-anonymous" {
		t.Fatalf("repository received account_id %q, want original session owner", repository.input.AccountID)
	}
	if detail.AccountID != "account-anonymous" {
		t.Fatalf("detail account_id = %q, want original session owner", detail.AccountID)
	}
}

func TestRecordRejectsMismatchedSessionOwnerWithoutCanonicalResolver(t *testing.T) {
	repository := &captureRepository{}
	input := validRecordInput()
	input.AccountID = "account-registered"

	_, err := NewPersistentUseCases(repository, staticSessionOwner{accountID: "account-anonymous"}).Record(t.Context(), input)

	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Record() error = %v, want forbidden", err)
	}
	if repository.input != (RecordInput{}) {
		t.Fatalf("repository input = %#v, want no persistence", repository.input)
	}
}

type captureRepository struct {
	input RecordInput
}

func (r *captureRepository) Record(_ context.Context, input RecordInput) (Detail, bool, error) {
	r.input = input
	return Detail{RecordInput: input}, true, nil
}

func (*captureRepository) SessionSummary(context.Context, string, string) (Summary, error) {
	return Summary{}, nil
}

func (*captureRepository) AccountSummary(context.Context, string, time.Time, time.Time) (Summary, error) {
	return Summary{}, nil
}

type mergedSessionOwner struct{}

func (mergedSessionOwner) AccountIDForSession(context.Context, string) (string, error) {
	return "account-anonymous", nil
}

func (mergedSessionOwner) CanonicalAccountID(_ context.Context, accountID string) (string, error) {
	if accountID == "account-anonymous" || accountID == "account-registered" {
		return "account-registered", nil
	}
	return accountID, nil
}

type staticSessionOwner struct{ accountID string }

func (o staticSessionOwner) AccountIDForSession(context.Context, string) (string, error) {
	return o.accountID, nil
}
