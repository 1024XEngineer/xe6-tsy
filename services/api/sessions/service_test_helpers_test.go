package sessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errDependency = errors.New("dependency failed")

type fakeIDGenerator struct {
	id    string
	calls int
}

func (f *fakeIDGenerator) NewVoiceSessionID() string {
	f.calls++
	return f.id
}

type fakeClock struct {
	now   time.Time
	calls int
}

func (f *fakeClock) Now() time.Time {
	f.calls++
	return f.now
}

type fakeRepository struct {
	createResult   VoiceSession
	createReplayed bool
	createErr      error
	createHook     func(context.Context)
	createParams   []CreateParams
}

var (
	_ Repository  = (*fakeRepository)(nil)
	_ IDGenerator = (*fakeIDGenerator)(nil)
	_ Clock       = (*fakeClock)(nil)
)

func (f *fakeRepository) Create(
	ctx context.Context,
	params CreateParams,
) (VoiceSession, bool, error) {
	f.createParams = append(f.createParams, params)
	if f.createHook != nil {
		f.createHook(ctx)
	}
	return f.createResult, f.createReplayed, f.createErr
}

func (f *fakeRepository) GetOwned(context.Context, string, string) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func (f *fakeRepository) List(context.Context, ListFilter) (ListPage, error) {
	return ListPage{}, ErrNotImplemented
}

func (f *fakeRepository) SaveEndIntent(context.Context, EndIntent) (EndIntent, bool, error) {
	return EndIntent{}, false, ErrNotImplemented
}

func (f *fakeRepository) GetEndIntent(context.Context, string, string) (EndIntent, error) {
	return EndIntent{}, ErrNotImplemented
}

func (f *fakeRepository) CompleteEndIntent(context.Context, string, string, time.Time) error {
	return ErrNotImplemented
}

func (f *fakeRepository) TransitionToActive(
	context.Context,
	StartTransitionParams,
) (VoiceSession, bool, error) {
	return VoiceSession{}, false, ErrNotImplemented
}

func (f *fakeRepository) TransitionToEnded(
	context.Context,
	EndTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func (f *fakeRepository) TransitionToFailed(
	context.Context,
	FailureTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func newCreateTestService(
	t *testing.T,
	repository Repository,
) (*Service, *fakeIDGenerator, *fakeClock) {
	t.Helper()
	ids := &fakeIDGenerator{id: "vs_generated"}
	clock := &fakeClock{now: time.Date(2026, 7, 27, 17, 0, 0, 0, time.FixedZone("CST", 8*60*60))}
	service, err := NewService(Dependencies{
		Repository: repository,
		IDs:        ids,
		Clock:      clock,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, ids, clock
}

func validCapabilities() Capabilities {
	return Capabilities{
		WebRTC:             true,
		DataChannel:        true,
		Microphone:         true,
		Speaker:            true,
		SpeakerDiarization: true,
	}
}
