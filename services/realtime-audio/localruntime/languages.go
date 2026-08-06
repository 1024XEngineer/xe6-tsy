package localruntime

import (
	"context"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

// StaticLanguageConfigReader returns a fixed bilingual config for local demos.
type StaticLanguageConfigReader struct {
	Source string
	Target string
	Now    func() time.Time
}

func (r StaticLanguageConfigReader) GetCurrentConfig(
	_ context.Context,
	sessionID string,
) (session.LanguageConfigSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.LanguageConfigSnapshot{}, session.ErrSessionIDRequired
	}
	source := strings.TrimSpace(r.Source)
	if source == "" {
		source = "zh-CN"
	}
	target := strings.TrimSpace(r.Target)
	if target == "" {
		target = "en-US"
	}
	now := r.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return session.LanguageConfigSnapshot{
		SessionID: sessionID,
		Version:   1,
		Status:    "active",
		LanguagePairs: []session.LanguagePair{
			{Source: source, Target: target},
			{Source: target, Target: source},
		},
		UpdatedAt: now(),
	}, nil
}
