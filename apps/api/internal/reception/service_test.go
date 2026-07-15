package reception

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type testSystem struct {
	service    *Service
	store      *MemoryStore
	authorizer *FakeAccessAuthorizer
	processing *FakeProcessingGate
	media      *FakeMediaAdapter
	cleaner    *FakeMediaResourceCleaner
}

func newTestSystem() *testSystem {
	store := NewMemoryStore()
	authorizer := &FakeAccessAuthorizer{}
	processing := DefaultFakeProcessingGate()
	media := NewFakeMediaAdapter()
	cleaner := &FakeMediaResourceCleaner{}
	service := NewService(store, authorizer, &FakeOrganizationConfigReader{}, processing, media, cleaner,
		&FixedClock{NowValue: time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)}, &SequentialIDGenerator{})
	return &testSystem{service, store, authorizer, processing, media, cleaner}
}

func createCommand(key string) CreateReceptionSessionCommand {
	return CreateReceptionSessionCommand{
		IdempotencyKey: key, AccessContextRef: "access-demo", OrganizationID: "trial-org",
		ServicePointID: "service-point-001", ServiceWindowID: "window-001",
		OrganizationConfigVersion: "config-v1", ProcessingContextRef: "processing-demo",
	}
}

func createSession(t *testing.T, system *testSystem, key string) ReceptionSessionView {
	t.Helper()
	view, err := system.service.CreateSession(context.Background(), createCommand(key))
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return view
}

func startSession(t *testing.T, system *testSystem, session ReceptionSessionView, key string) StartReceptionSessionResult {
	t.Helper()
	result, err := system.service.StartSession(context.Background(), StartReceptionSessionCommand{
		SessionID: session.SessionID, AccessContextRef: "access-demo", ExpectedVersion: session.Version, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	return result
}

func attachMedia(t *testing.T, system *testSystem, session ReceptionSessionView, key, scenario string) (AttachFakeMediaResult, error) {
	t.Helper()
	return system.service.AttachFakeMedia(context.Background(), AttachFakeMediaTrackCommand{
		SessionID: session.SessionID, AccessContextRef: "access-demo", ExpectedSessionVersion: session.Version,
		IdempotencyKey: key, TrackRef: "fake-track-001", Scenario: scenario,
	})
}

func errorCode(t *testing.T, err error) string {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) {
		t.Fatalf("error = %v, want *Error", err)
	}
	return target.Code
}

func TestReceptionFullLifecycle(t *testing.T) {
	system := newTestSystem()
	created := createSession(t, system, "create-full")
	if created.Status != ReceptionSessionCreated || created.Version != 1 {
		t.Fatalf("created = %#v", created)
	}
	started := startSession(t, system, created, "start-full")
	attached, err := attachMedia(t, system, started.Session, "attach-full", FakeScenarioSuccess)
	if err != nil {
		t.Fatalf("AttachFakeMedia() error = %v", err)
	}
	if attached.Binding.Status != MediaTrackAttached || attached.Binding.Version != 2 {
		t.Fatalf("attached binding = %#v", attached.Binding)
	}
	detached, err := system.service.DetachMedia(context.Background(), DetachMediaTrackCommand{
		SessionID: created.SessionID, BindingID: attached.Binding.BindingID, AccessContextRef: "access-demo",
		ExpectedBindingVersion: attached.Binding.Version, IdempotencyKey: "detach-full",
	})
	if err != nil || detached.Status != MediaTrackDetached || detached.Version != 3 {
		t.Fatalf("DetachMedia() = %#v, %v", detached, err)
	}
	ended, err := system.service.EndSession(context.Background(), EndReceptionSessionCommand{
		SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: started.Session.Version, IdempotencyKey: "end-full",
	})
	if err != nil || ended.Status != ReceptionSessionEnded || ended.Version != 3 {
		t.Fatalf("EndSession() = %#v, %v", ended, err)
	}
	wantEvents := []string{EventReceptionSessionCreated, EventReceptionSessionStarted, EventMediaTrackAttached, EventMediaTrackDetached, EventReceptionSessionEnded}
	events := system.store.Events()
	for index, want := range wantEvents {
		if events[index].EventType != want {
			t.Fatalf("event %d = %q, want %q", index, events[index].EventType, want)
		}
	}
	if len(system.store.Audits()) != len(wantEvents) || system.cleaner.Count() != 1 {
		t.Fatalf("audits = %d, cleaner calls = %d", len(system.store.Audits()), system.cleaner.Count())
	}
}

func TestAttachFailureKeepsSessionActive(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-af"), "start-af")
	result, err := attachMedia(t, system, started.Session, "attach-af", FakeScenarioAttachFailure)
	if errorCode(t, err) != CodeMediaAttachFailed || result.Binding.Status != MediaTrackFailed || result.Degradation == nil || !result.Degradation.SessionRemainsActive {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	view, _ := system.service.GetSession(context.Background(), started.Session.SessionID)
	if view.Status != ReceptionSessionActive || !view.MediaCapability.ManualTextAvailable {
		t.Fatalf("session after failure = %#v", view)
	}
	if got := system.store.Events()[2].EventType; got != EventMediaTrackUnavailable {
		t.Fatalf("event = %q", got)
	}
	if got := system.store.Audits()[2].Result; got != "degraded" {
		t.Fatalf("audit result = %q, want degraded", got)
	}
}

func TestRuntimeDisconnectKeepsSessionActive(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-rd"), "start-rd")
	attached, err := attachMedia(t, system, started.Session, "attach-rd", FakeScenarioRuntimeDisconnect)
	if err != nil {
		t.Fatal(err)
	}
	disconnected, err := system.service.HandleRuntimeDisconnect(context.Background(), attached.Binding.BindingID)
	if err != nil || disconnected.Binding.Status != MediaTrackFailed || system.cleaner.Count() != 1 {
		t.Fatalf("disconnect = %#v, %v", disconnected, err)
	}
	view, _ := system.service.GetSession(context.Background(), started.Session.SessionID)
	if view.Status != ReceptionSessionActive {
		t.Fatalf("session status = %q", view.Status)
	}
	if _, err := attachMedia(t, system, started.Session, "attach-retry", FakeScenarioSuccess); err != nil {
		t.Fatalf("new binding after disconnect: %v", err)
	}
}

func TestDetachFailureDoesNotFakeDetachedState(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-df"), "start-df")
	attached, _ := attachMedia(t, system, started.Session, "attach-df", FakeScenarioDetachFailure)
	_, err := system.service.DetachMedia(context.Background(), DetachMediaTrackCommand{
		SessionID: started.Session.SessionID, BindingID: attached.Binding.BindingID, AccessContextRef: "access-demo",
		ExpectedBindingVersion: attached.Binding.Version, IdempotencyKey: "detach-df",
	})
	if errorCode(t, err) != CodeMediaDetachFailed {
		t.Fatal(err)
	}
	binding, _ := system.store.GetBinding(context.Background(), attached.Binding.BindingID)
	session, _ := system.store.GetSession(context.Background(), started.Session.SessionID)
	if binding.Status != MediaTrackAttached || session.Status != ReceptionSessionActive {
		t.Fatalf("binding = %q, session = %q", binding.Status, session.Status)
	}
}

func TestCannotAttachMediaToCreatedOrEndedSession(t *testing.T) {
	for _, target := range []string{"created", "ended"} {
		t.Run(target, func(t *testing.T) {
			system := newTestSystem()
			created := createSession(t, system, "create-"+target)
			view := created
			if target == "ended" {
				started := startSession(t, system, created, "start-ended")
				view, _ = system.service.EndSession(context.Background(), EndReceptionSessionCommand{SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: started.Session.Version, IdempotencyKey: "end-ended"})
			}
			_, err := attachMedia(t, system, view, "attach-"+target, FakeScenarioSuccess)
			if errorCode(t, err) != CodeInvalidSessionState {
				t.Fatal(err)
			}
		})
	}
}

func TestOnlyOneActiveBindingPerSession(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-one"), "start-one")
	if _, err := attachMedia(t, system, started.Session, "attach-one-a", FakeScenarioSuccess); err != nil {
		t.Fatal(err)
	}
	_, err := attachMedia(t, system, started.Session, "attach-one-b", FakeScenarioSuccess)
	if errorCode(t, err) != CodeActiveMediaBindingExists {
		t.Fatal(err)
	}
}

func TestVersionMismatchRejectsUpdate(t *testing.T) {
	system := newTestSystem()
	created := createSession(t, system, "create-version")
	_, err := system.service.StartSession(context.Background(), StartReceptionSessionCommand{SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: 99, IdempotencyKey: "start-version"})
	if errorCode(t, err) != CodeVersionMismatch {
		t.Fatal(err)
	}
}

func TestCreateSessionIdempotency(t *testing.T) {
	system := newTestSystem()
	first := createSession(t, system, "same-create")
	second := createSession(t, system, "same-create")
	if first.SessionID != second.SessionID || system.store.SessionCount() != 1 || len(system.store.Events()) != 1 {
		t.Fatalf("first = %q, second = %q, count = %d", first.SessionID, second.SessionID, system.store.SessionCount())
	}
}

func TestAttachMediaIdempotency(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-ai"), "start-ai")
	first, _ := attachMedia(t, system, started.Session, "same-attach", FakeScenarioSuccess)
	second, _ := attachMedia(t, system, started.Session, "same-attach", FakeScenarioSuccess)
	if first.Binding.BindingID != second.Binding.BindingID || system.store.BindingCount() != 1 || system.media.AttachCalls != 1 {
		t.Fatalf("first = %#v second = %#v calls = %d", first, second, system.media.AttachCalls)
	}
}

func TestIdempotencyConflict(t *testing.T) {
	system := newTestSystem()
	createSession(t, system, "conflict")
	command := createCommand("conflict")
	command.ServiceWindowID = "another-window"
	_, err := system.service.CreateSession(context.Background(), command)
	if errorCode(t, err) != CodeIdempotencyConflict {
		t.Fatal(err)
	}
}

func TestAccessDeniedDoesNotModifyState(t *testing.T) {
	system := newTestSystem()
	system.authorizer.Denied = true
	_, err := system.service.CreateSession(context.Background(), createCommand("denied"))
	if errorCode(t, err) != CodeAccessDenied || system.store.SessionCount() != 0 || len(system.store.Events()) != 0 {
		t.Fatalf("error = %v, sessions = %d", err, system.store.SessionCount())
	}
}

func TestReceptionNotAllowed(t *testing.T) {
	system := newTestSystem()
	system.processing.ReceptionAllowed = false
	_, err := system.service.CreateSession(context.Background(), createCommand("gate-denied"))
	if errorCode(t, err) != CodeReceptionNotAllowed {
		t.Fatal(err)
	}
}

func TestRealtimeAudioNotAllowedButSessionCanRemainActive(t *testing.T) {
	system := newTestSystem()
	system.processing.RealtimeAudioAllowed = false
	started := startSession(t, system, createSession(t, system, "create-noaudio"), "start-noaudio")
	if started.Session.Status != ReceptionSessionActive || started.MediaCapability.AudioTrackAllowed || !started.MediaCapability.ManualTextAvailable {
		t.Fatalf("result = %#v", started)
	}
	_, err := attachMedia(t, system, started.Session, "attach-noaudio", FakeScenarioSuccess)
	if errorCode(t, err) != CodeRealtimeAudioNotAllowed {
		t.Fatal(err)
	}
}

func TestRecordingPersistenceDisabledDoesNotBlockStart(t *testing.T) {
	system := newTestSystem()
	system.processing.RecordingPersistenceAllowed = false
	started := startSession(t, system, createSession(t, system, "create-norecord"), "start-norecord")
	if started.Session.Status != ReceptionSessionActive {
		t.Fatalf("status = %q", started.Session.Status)
	}
}

func TestEndedSessionCannotRestart(t *testing.T) {
	system := newTestSystem()
	created := createSession(t, system, "create-terminal-end")
	started := startSession(t, system, created, "start-terminal-end")
	ended, _ := system.service.EndSession(context.Background(), EndReceptionSessionCommand{SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: started.Session.Version, IdempotencyKey: "end-terminal"})
	_, err := system.service.StartSession(context.Background(), StartReceptionSessionCommand{SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: ended.Version, IdempotencyKey: "restart-ended"})
	if errorCode(t, err) != CodeInvalidSessionState {
		t.Fatal(err)
	}
}

func TestCancelledSessionCannotRestart(t *testing.T) {
	system := newTestSystem()
	created := createSession(t, system, "create-terminal-cancel")
	cancelled, _ := system.service.CancelSession(context.Background(), CancelReceptionSessionCommand{SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: created.Version, IdempotencyKey: "cancel-terminal", ReasonCode: "created_by_mistake"})
	_, err := system.service.StartSession(context.Background(), StartReceptionSessionCommand{SessionID: created.SessionID, AccessContextRef: "access-demo", ExpectedVersion: cancelled.Version, IdempotencyKey: "restart-cancelled"})
	if errorCode(t, err) != CodeInvalidSessionState {
		t.Fatal(err)
	}
}

func TestDetachedBindingCannotReattach(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-detached"), "start-detached")
	attached, _ := attachMedia(t, system, started.Session, "attach-detached", FakeScenarioSuccess)
	detached, _ := system.service.DetachMedia(context.Background(), DetachMediaTrackCommand{SessionID: started.Session.SessionID, BindingID: attached.Binding.BindingID, AccessContextRef: "access-demo", ExpectedBindingVersion: attached.Binding.Version, IdempotencyKey: "detach-terminal"})
	_, err := system.service.DetachMedia(context.Background(), DetachMediaTrackCommand{SessionID: started.Session.SessionID, BindingID: attached.Binding.BindingID, AccessContextRef: "access-demo", ExpectedBindingVersion: detached.Version, IdempotencyKey: "detach-again"})
	if errorCode(t, err) != CodeInvalidBindingState {
		t.Fatal(err)
	}
}

func TestFailedBindingCannotBecomeAttached(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-failed"), "start-failed")
	failed, _ := attachMedia(t, system, started.Session, "attach-failed", FakeScenarioAttachFailure)
	_, err := system.service.HandleRuntimeDisconnect(context.Background(), failed.Binding.BindingID)
	if errorCode(t, err) != CodeInvalidBindingState {
		t.Fatal(err)
	}
}

func TestEventsDoNotContainRawAudio(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-safe"), "start-safe")
	_, _ = attachMedia(t, system, started.Session, "attach-safe", FakeScenarioSuccess)
	encoded, _ := json.Marshal(system.store.Events())
	for _, forbidden := range []string{"raw_audio", "audio_bytes", "token", "secret"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("events contain forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestNoRawAudioIsPersisted(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-zeroaudio"), "start-zeroaudio")
	_, _ = attachMedia(t, system, started.Session, "attach-zeroaudio", FakeScenarioSuccess)
	encoded, _ := json.Marshal(struct {
		Events   []DomainEvent
		Sessions int
		Bindings int
	}{system.store.Events(), system.store.SessionCount(), system.store.BindingCount()})
	if strings.Contains(strings.ToLower(string(encoded)), "audio_bytes") {
		t.Fatalf("persisted state contains raw audio: %s", encoded)
	}
}

func TestEndWithActiveBindingWritesCleanupEvent(t *testing.T) {
	system := newTestSystem()
	started := startSession(t, system, createSession(t, system, "create-end-active"), "start-end-active")
	attached, err := attachMedia(t, system, started.Session, "attach-end-active", FakeScenarioSuccess)
	if err != nil {
		t.Fatal(err)
	}
	ended, err := system.service.EndSession(context.Background(), EndReceptionSessionCommand{
		SessionID: started.Session.SessionID, AccessContextRef: "access-demo",
		ExpectedVersion: started.Session.Version, IdempotencyKey: "end-with-active",
	})
	if err != nil || ended.Status != ReceptionSessionEnded {
		t.Fatalf("EndSession() = %#v, %v", ended, err)
	}
	binding, _ := system.store.GetBinding(context.Background(), attached.Binding.BindingID)
	if binding.Status != MediaTrackFailed || system.cleaner.Count() != 1 {
		t.Fatalf("binding = %q, cleaner = %d", binding.Status, system.cleaner.Count())
	}
	events := system.store.Events()
	if events[len(events)-2].EventType != EventMediaTrackUnavailable || events[len(events)-1].EventType != EventReceptionSessionEnded || events[len(events)-1].AggregateType != "ReceptionSession" {
		t.Fatalf("cleanup events = %#v", events[len(events)-2:])
	}
}

func TestCommitFailureDoesNotReturnBusinessSuccess(t *testing.T) {
	system := newTestSystem()
	system.store.SetCommitFailure(true)
	_, err := system.service.CreateSession(context.Background(), createCommand("commit-failure"))
	if errorCode(t, err) != CodeInternalError || system.store.SessionCount() != 0 || len(system.store.Events()) != 0 || len(system.store.Audits()) != 0 {
		t.Fatalf("error = %v sessions = %d events = %d audits = %d", err, system.store.SessionCount(), len(system.store.Events()), len(system.store.Audits()))
	}
}
