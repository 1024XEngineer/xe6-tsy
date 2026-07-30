package sessions

import "time"

// SystemClock is the production wall-clock adapter. Session timestamps are
// stored and compared in UTC.
type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}
