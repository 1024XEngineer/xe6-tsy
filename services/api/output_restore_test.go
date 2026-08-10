package main

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
)

func TestLanguageOutputRestorerCreatesBidirectionalConfig(t *testing.T) {
	service := &languageOutputServiceFake{configs: []languages.LanguageConfig{{
		SessionID: "session-1",
		Version:   3,
		LanguagePairs: []languages.LanguagePair{
			{Source: "zh-CN", Target: "en-US"},
			{Source: "en-US", Target: "zh-CN"},
		},
		OutputRoutes: []languages.OutputRoute{
			{TargetLanguage: "en-US", TTSEnabled: true},
			{TargetLanguage: "zh-CN", DeliveryEnabled: true},
		},
	}}}
	restorer := languageOutputRestorer{service: service}

	if err := restorer.RestoreBidirectionalOutput(t.Context(), "account-1", "session-1", 3, "restore-fallback-1"); err != nil {
		t.Fatalf("RestoreBidirectionalOutput() error = %v", err)
	}
	if service.createCalls != 1 || service.accountID != "account-1" || service.sessionID != "session-1" || service.operationID != "restore-fallback-1" {
		t.Fatalf("CreateConfig() input = %#v", service)
	}
	if service.request.ExpectedVersion == nil || *service.request.ExpectedVersion != 3 {
		t.Fatalf("expected version = %#v", service.request.ExpectedVersion)
	}
	if len(service.request.OutputRoutes) != 2 {
		t.Fatalf("output routes = %#v", service.request.OutputRoutes)
	}
	for _, route := range service.request.OutputRoutes {
		if !route.TTSEnabled || route.DeliveryEnabled {
			t.Fatalf("restored output route = %#v", route)
		}
	}
}

func TestLanguageOutputRestorerDoesNotOverrideNewerConfig(t *testing.T) {
	service := &languageOutputServiceFake{configs: []languages.LanguageConfig{{Version: 4}}}
	restorer := languageOutputRestorer{service: service}

	if err := restorer.RestoreBidirectionalOutput(t.Context(), "account-1", "session-1", 3, "restore-fallback-1"); err != nil {
		t.Fatalf("RestoreBidirectionalOutput() error = %v", err)
	}
	if service.createCalls != 0 {
		t.Fatalf("CreateConfig() calls = %d, want 0", service.createCalls)
	}
}

func TestLanguageOutputRestorerAcceptsNewerConfigAfterVersionConflict(t *testing.T) {
	service := &languageOutputServiceFake{
		configs: []languages.LanguageConfig{
			{
				Version: 3,
				LanguagePairs: []languages.LanguagePair{
					{Source: "zh-CN", Target: "en-US"},
					{Source: "en-US", Target: "zh-CN"},
				},
				OutputRoutes: []languages.OutputRoute{
					{TargetLanguage: "en-US", TTSEnabled: true},
					{TargetLanguage: "zh-CN", DeliveryEnabled: true},
				},
			},
			{Version: 4},
		},
		createErr: languages.ErrVersionConflict,
	}
	restorer := languageOutputRestorer{service: service}

	if err := restorer.RestoreBidirectionalOutput(t.Context(), "account-1", "session-1", 3, "restore-fallback-1"); err != nil {
		t.Fatalf("RestoreBidirectionalOutput() error = %v", err)
	}
	if service.getCalls != 2 || service.createCalls != 1 {
		t.Fatalf("calls = get %d, create %d", service.getCalls, service.createCalls)
	}
}

type languageOutputServiceFake struct {
	configs     []languages.LanguageConfig
	getCalls    int
	createCalls int
	accountID   string
	sessionID   string
	operationID string
	request     languages.CreateLanguageConfigRequest
	createErr   error
}

func (f *languageOutputServiceFake) GetActiveConfig(context.Context, string, string) (languages.LanguageConfig, error) {
	if len(f.configs) == 0 {
		return languages.LanguageConfig{}, errors.New("no configured language config")
	}
	index := min(f.getCalls, len(f.configs)-1)
	f.getCalls++
	return f.configs[index], nil
}

func (f *languageOutputServiceFake) CreateConfig(_ context.Context, accountID, sessionID, operationID string, request languages.CreateLanguageConfigRequest) (languages.LanguageConfig, error) {
	f.createCalls++
	f.accountID = accountID
	f.sessionID = sessionID
	f.operationID = operationID
	f.request = request
	return languages.LanguageConfig{}, f.createErr
}
