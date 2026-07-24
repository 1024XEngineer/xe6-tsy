package usage

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type UseCases struct{}

func NewUseCases() *UseCases { return &UseCases{} }

func (*UseCases) Record(context.Context, RecordInput) (Detail, error) {
	return Detail{}, domain.ErrNotImplemented
}

func (*UseCases) SessionUsage(context.Context, string, string) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}

func (*UseCases) AccountUsage(context.Context, string, time.Time, time.Time) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}
