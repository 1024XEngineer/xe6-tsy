package sessions

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ULIDGenerator creates prefixed session and start-operation identities.
type ULIDGenerator struct {
	entropy *ulid.LockedMonotonicReader
}

// NewULIDGenerator returns a concurrency-safe production ID generator.
func NewULIDGenerator() *ULIDGenerator {
	return &ULIDGenerator{
		entropy: &ulid.LockedMonotonicReader{
			MonotonicReader: ulid.Monotonic(rand.Reader, 0),
		},
	}
}

func (g *ULIDGenerator) NewVoiceSessionID() string {
	return "vs_" + g.newID()
}

func (g *ULIDGenerator) NewStartOperationID() string {
	return "op_" + g.newID()
}

func (g *ULIDGenerator) newID() string {
	if g == nil || g.entropy == nil {
		return ulid.Make().String()
	}
	id, err := ulid.New(ulid.Timestamp(time.Now().UTC()), g.entropy)
	if err != nil {
		return fmt.Sprintf("%s%x", ulid.Make().String(), time.Now().UnixNano())
	}
	return id.String()
}
