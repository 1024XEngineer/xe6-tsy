package sessions

import (
	"fmt"
	"time"
)

// nowUTC rejects unusable dependency output before any lifecycle timestamp is
// persisted. The stage identifies which durable transition could not proceed.
func (s *Service) nowUTC(stage string) (time.Time, error) {
	now := s.deps.Clock.Now()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf(
			"%w: clock returned a zero timestamp for %s",
			ErrInvalidDependency,
			stage,
		)
	}
	return now.UTC(), nil
}
