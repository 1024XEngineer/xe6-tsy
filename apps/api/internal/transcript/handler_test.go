package transcript

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandler_StartRun(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{
			name:       "valid request",
			body:       `{"operator_id":"staff-demo","organization_id":"trial-org","organization_config_version":"config-v1","session_id":"session-demo","track_id":"track-demo","direction":"citizen_to_worker","source_language":"zh-sichuan","target_language":"zh-cmn","tts_mode":"off"}`,
			wantStatus: http.StatusCreated,
			wantCalls:  1,
		},
		{
			name:       "invalid json",
			body:       `{`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REQUEST",
		},
		{
			name:       "dependency unavailable",
			body:       `{"operator_id":"staff-demo","organization_id":"trial-org","organization_config_version":"config-v1","session_id":"session-demo","track_id":"track-demo","direction":"citizen_to_worker","source_language":"zh-sichuan","target_language":"zh-cmn","tts_mode":"off"}`,
			serviceErr: ErrDependencyUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "DEPENDENCY_UNAVAILABLE",
			wantCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			service := &stubService{err: tt.serviceErr}
			router := gin.New()
			NewHandler(service).Register(router.Group("/api/v1/speech"))

			req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/runs", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)

			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", res.Code, tt.wantStatus, res.Body.String())
			}
			if service.startCalls != tt.wantCalls {
				t.Fatalf("StartRun calls = %d, want %d", service.startCalls, tt.wantCalls)
			}
			if tt.wantCode != "" && !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s, want error code %s", res.Body.String(), tt.wantCode)
			}
		})
	}
}

type stubService struct {
	err        error
	startCalls int
}

func (s *stubService) StartRun(_ context.Context, cmd StartRunCommand) (SpeechProcessingRun, error) {
	s.startCalls++
	if s.err != nil {
		return SpeechProcessingRun{}, s.err
	}
	return SpeechProcessingRun{
		ID:            "run-demo",
		SessionID:     cmd.SessionID,
		TrackID:       cmd.TrackID,
		ConfigVersion: cmd.ConfigVersion,
		Status:        RunStatusStreaming,
	}, nil
}

func (s *stubService) CompleteRun(context.Context, string) (SpeechProcessingRun, error) {
	return SpeechProcessingRun{}, errors.New("not used")
}

func (s *stubService) ListUtterances(context.Context, string) ([]SpeechUtterance, error) {
	return nil, errors.New("not used")
}

func (s *stubService) AssignSpeakerRole(context.Context, AssignSpeakerRoleCommand) (SpeakerRoleBinding, error) {
	return SpeakerRoleBinding{}, errors.New("not used")
}

func (s *stubService) RequestTTS(context.Context, RequestTTSCommand) (TtsSynthesisRun, error) {
	return TtsSynthesisRun{}, errors.New("not used")
}
