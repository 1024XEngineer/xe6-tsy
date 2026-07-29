package usage

import (
	"context"
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

func TestValidateAllowsMissingPricingIndependently(t *testing.T) {
	for name, input := range map[string]RecordInput{
		"both missing": validRecordInput(),
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
			if err := validate(input); err != nil {
				t.Fatalf("validate() error = %v", err)
			}
		})
	}
}

func TestValidateRejectsMalformedPricing(t *testing.T) {
	for name, input := range map[string]RecordInput{
		"negative cost": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "-1"
			return input
		}(),
		"leading zero": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "00.1"
			return input
		}(),
		"exponent": func() RecordInput {
			input := validRecordInput()
			input.CostAmount = "1e-3"
			return input
		}(),
		"lowercase currency": func() RecordInput {
			input := validRecordInput()
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
