package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
)

type languageOutputService interface {
	GetActiveConfig(context.Context, string, string) (languages.LanguageConfig, error)
	CreateConfig(context.Context, string, string, string, languages.CreateLanguageConfigRequest) (languages.LanguageConfig, error)
}

type languageOutputRestorer struct {
	service languageOutputService
}

func (r languageOutputRestorer) RestoreBidirectionalOutput(ctx context.Context, accountID, sessionID string, expectedVersion int, operationID string) error {
	if r.service == nil || accountID == "" || sessionID == "" || expectedVersion < 1 || operationID == "" {
		return languages.ErrInvalidRequest
	}
	current, err := r.service.GetActiveConfig(ctx, accountID, sessionID)
	if err != nil {
		return err
	}
	// A newer version represents a later user choice and must win over the
	// automatic restore for the failed Turn.
	if current.Version > expectedVersion || bidirectionalTTS(current.OutputRoutes) {
		return nil
	}
	if current.Version != expectedVersion {
		return languages.ErrVersionConflict
	}
	routes := make([]languages.OutputRoute, 0, len(current.LanguagePairs))
	seen := make(map[string]struct{}, len(current.LanguagePairs))
	for _, pair := range current.LanguagePairs {
		if _, exists := seen[pair.Target]; exists {
			continue
		}
		seen[pair.Target] = struct{}{}
		routes = append(routes, languages.OutputRoute{TargetLanguage: pair.Target, TTSEnabled: true})
	}
	version := current.Version
	_, err = r.service.CreateConfig(ctx, accountID, sessionID, operationID, languages.CreateLanguageConfigRequest{
		Languages: current.LanguagePairs, OutputRoutes: routes, ExpectedVersion: &version,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, languages.ErrVersionConflict) {
		return err
	}
	latest, readErr := r.service.GetActiveConfig(ctx, accountID, sessionID)
	if readErr == nil && latest.Version > expectedVersion {
		return nil
	}
	return fmt.Errorf("restore output version %d: %w", expectedVersion, err)
}

func bidirectionalTTS(routes []languages.OutputRoute) bool {
	if len(routes) != 2 {
		return false
	}
	for _, route := range routes {
		if !route.TTSEnabled || route.DeliveryEnabled {
			return false
		}
	}
	return true
}
