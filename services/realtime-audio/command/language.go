package command

import (
	"context"
	"errors"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
)

var ErrDeliveryTargetRequired = errors.New("automatic delivery target is required")

// LanguageConfigurator asks the API control plane to create or replay an API-owned language
// configuration. Implementations must use CommandID as the idempotency identity.
type LanguageConfigurator interface {
	Configure(context.Context, languagesv1.CommandConfigRequest) (languagesv1.CommandConfigResult, error)
}

// LanguageConfiguratorFunc adapts a function to the language-configuration boundary.
type LanguageConfiguratorFunc func(context.Context, languagesv1.CommandConfigRequest) (languagesv1.CommandConfigResult, error)

func (f LanguageConfiguratorFunc) Configure(ctx context.Context, request languagesv1.CommandConfigRequest) (languagesv1.CommandConfigResult, error) {
	return f(ctx, request)
}
