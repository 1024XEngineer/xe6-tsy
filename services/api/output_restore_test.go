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

func TestLanguageOutputRestorerRejectsInvalidInput(t *testing.T) {
	validService := &languageOutputServiceFake{}
	tests := []struct {
		name            string
		restorer        languageOutputRestorer
		accountID       string
		sessionID       string
		expectedVersion int
		operationID     string
	}{
		{name: "missing service", accountID: "account-1", sessionID: "session-1", expectedVersion: 1, operationID: "operation-1"},
		{name: "missing account", restorer: languageOutputRestorer{service: validService}, sessionID: "session-1", expectedVersion: 1, operationID: "operation-1"},
		{name: "missing session", restorer: languageOutputRestorer{service: validService}, accountID: "account-1", expectedVersion: 1, operationID: "operation-1"},
		{name: "zero version", restorer: languageOutputRestorer{service: validService}, accountID: "account-1", sessionID: "session-1", operationID: "operation-1"},
		{name: "missing operation", restorer: languageOutputRestorer{service: validService}, accountID: "account-1", sessionID: "session-1", expectedVersion: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.restorer.RestoreBidirectionalOutput(t.Context(), test.accountID, test.sessionID, test.expectedVersion, test.operationID)
			if !errors.Is(err, languages.ErrInvalidRequest) {
				t.Fatalf("RestoreBidirectionalOutput() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestLanguageOutputRestorerPreservesReadAndCreateFailures(t *testing.T) {
	readErr := errors.New("read config")
	createErr := errors.New("write config")
	config := languageOutputConfig(3, []languages.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}})
	tests := []struct {
		name            string
		service         *languageOutputServiceFake
		expectedVersion int
		wantErr         error
		wantCreates     int
	}{
		{name: "read failure", service: &languageOutputServiceFake{getErrs: []error{readErr}}, expectedVersion: 4, wantErr: readErr},
		{name: "version mismatch", service: &languageOutputServiceFake{configs: []languages.LanguageConfig{config}}, expectedVersion: 4, wantErr: languages.ErrVersionConflict},
		{name: "create failure", service: &languageOutputServiceFake{configs: []languages.LanguageConfig{config}, createErr: createErr}, expectedVersion: 3, wantErr: createErr, wantCreates: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (languageOutputRestorer{service: test.service}).RestoreBidirectionalOutput(t.Context(), "account-1", "session-1", test.expectedVersion, "operation-1")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("RestoreBidirectionalOutput() error = %v, want %v", err, test.wantErr)
			}
			if test.service.createCalls != test.wantCreates {
				t.Fatalf("CreateConfig() calls = %d, want %d", test.service.createCalls, test.wantCreates)
			}
		})
	}
}

func TestLanguageOutputRestorerReportsVersionConflictWhenRereadFails(t *testing.T) {
	conflict := languages.ErrVersionConflict
	service := &languageOutputServiceFake{
		configs:   []languages.LanguageConfig{languageOutputConfig(3, []languages.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}})},
		getErrs:   []error{nil, errors.New("reread config")},
		createErr: conflict,
	}

	err := (languageOutputRestorer{service: service}).RestoreBidirectionalOutput(t.Context(), "account-1", "session-1", 3, "operation-1")
	if !errors.Is(err, conflict) {
		t.Fatalf("RestoreBidirectionalOutput() error = %v, want version conflict", err)
	}
	if service.getCalls != 2 || service.createCalls != 1 {
		t.Fatalf("calls = get %d, create %d, want get 2, create 1", service.getCalls, service.createCalls)
	}
}

func TestLanguageOutputRestorerBuildsTTSDirectionsFromLanguagePairs(t *testing.T) {
	tests := []struct {
		name          string
		pairs         []languages.LanguagePair
		wantLanguages []string
	}{
		{
			name: "bidirectional pairs",
			pairs: []languages.LanguagePair{
				{Source: "zh-CN", Target: "en-US"},
				{Source: "en-US", Target: "zh-CN"},
			},
			wantLanguages: []string{"en-US", "zh-CN"},
		},
		{
			name: "duplicate targets",
			pairs: []languages.LanguagePair{
				{Source: "zh-CN", Target: "en-US"},
				{Source: "fr-FR", Target: "en-US"},
				{Source: "en-US", Target: "zh-CN"},
			},
			wantLanguages: []string{"en-US", "zh-CN"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &languageOutputServiceFake{configs: []languages.LanguageConfig{{
				Version:       3,
				LanguagePairs: test.pairs,
				OutputRoutes:  []languages.OutputRoute{{TargetLanguage: "en-US", DeliveryEnabled: true}},
			}}}

			if err := (languageOutputRestorer{service: service}).RestoreBidirectionalOutput(t.Context(), "account-1", "session-1", 3, "operation-1"); err != nil {
				t.Fatalf("RestoreBidirectionalOutput() error = %v", err)
			}
			if len(service.request.OutputRoutes) != len(test.wantLanguages) {
				t.Fatalf("output routes = %#v, want %d routes", service.request.OutputRoutes, len(test.wantLanguages))
			}
			for index, language := range test.wantLanguages {
				route := service.request.OutputRoutes[index]
				if route.TargetLanguage != language || !route.TTSEnabled || route.DeliveryEnabled {
					t.Fatalf("output route %d = %#v, want TTS-only route for %q", index, route, language)
				}
			}
		})
	}
}

func languageOutputConfig(version int, routes []languages.OutputRoute) languages.LanguageConfig {
	return languages.LanguageConfig{
		Version: version,
		LanguagePairs: []languages.LanguagePair{
			{Source: "zh-CN", Target: "en-US"},
			{Source: "en-US", Target: "zh-CN"},
		},
		OutputRoutes: routes,
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
	getErrs     []error
}

func (f *languageOutputServiceFake) GetActiveConfig(context.Context, string, string) (languages.LanguageConfig, error) {
	index := min(f.getCalls, len(f.configs)-1)
	if len(f.getErrs) > 0 {
		errIndex := min(f.getCalls, len(f.getErrs)-1)
		f.getCalls++
		if err := f.getErrs[errIndex]; err != nil {
			return languages.LanguageConfig{}, err
		}
		if index < 0 {
			return languages.LanguageConfig{}, errors.New("no configured language config")
		}
		return f.configs[index], nil
	}
	if len(f.configs) == 0 {
		return languages.LanguageConfig{}, errors.New("no configured language config")
	}
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
