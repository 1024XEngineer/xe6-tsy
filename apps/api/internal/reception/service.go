package reception

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

type Service struct {
	mu         sync.Mutex
	store      TransactionalStore
	authorizer AccessAuthorizer
	configs    OrganizationConfigReader
	processing ProcessingGate
	media      MediaAdapter
	cleaner    MediaResourceCleaner
	clock      Clock
	ids        IDGenerator
}

func NewService(
	store TransactionalStore,
	authorizer AccessAuthorizer,
	configs OrganizationConfigReader,
	processing ProcessingGate,
	media MediaAdapter,
	cleaner MediaResourceCleaner,
	clock Clock,
	ids IDGenerator,
) *Service {
	return &Service{store: store, authorizer: authorizer, configs: configs, processing: processing,
		media: media, cleaner: cleaner, clock: clock, ids: ids}
}

func (s *Service) CreateSession(ctx context.Context, command CreateReceptionSessionCommand) (ReceptionSessionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateCreate(command); err != nil {
		return ReceptionSessionView{}, err
	}
	fingerprint := fingerprint(command)
	if replay, err := s.replay(ctx, "create", command.IdempotencyKey, fingerprint); replay != nil || err != nil {
		if err != nil {
			return ReceptionSessionView{}, err
		}
		return replay.Result.(ReceptionSessionView), nil
	}
	access, err := s.authorize(ctx, command.AccessContextRef, "reception:create", command.OrganizationID, command.ServicePointID, command.ServiceWindowID)
	if err != nil {
		return ReceptionSessionView{}, err
	}
	if _, err := s.configs.GetPublishedConfig(ctx, PublishedConfigRequest{
		OrganizationID: command.OrganizationID, ServicePointID: command.ServicePointID,
		ServiceWindowID: command.ServiceWindowID, ExpectedVersion: command.OrganizationConfigVersion,
	}); err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	processing, err := s.processingContext(ctx, command.ProcessingContextRef)
	if err != nil {
		return ReceptionSessionView{}, err
	}
	if !processing.ReceptionAllowed {
		return ReceptionSessionView{}, businessError(CodeReceptionNotAllowed, "当前处理上下文不允许开始接待。")
	}
	now := s.clock.Now()
	session := ReceptionSession{
		SessionID: s.ids.NewID("session"), OperatorID: access.OperatorID, OrganizationID: command.OrganizationID,
		ServicePointID: command.ServicePointID, ServiceWindowID: command.ServiceWindowID,
		OrganizationConfigVersion: command.OrganizationConfigVersion, ProcessingContextRef: command.ProcessingContextRef,
		Status: ReceptionSessionCreated, Version: 1, CreatedAt: now,
	}
	view := sessionView(session, nil, capability(processing))
	mutation := s.mutation("create", command.IdempotencyKey, fingerprint, view, session, nil, EventReceptionSessionCreated, "reception:create", now)
	if err := s.store.Commit(ctx, mutation); err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	return view, nil
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (ReceptionSessionView, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	bindings, err := s.store.BindingsBySession(ctx, sessionID)
	if err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	processing, err := s.processing.GetProcessingContext(ctx, session.ProcessingContextRef)
	if err != nil || processing.Expired {
		processing = ProcessingContextView{ReceptionAllowed: true, RealtimeAudioAllowed: false}
	}
	return sessionView(session, bindings, capability(processing)), nil
}

func (s *Service) StartSession(ctx context.Context, command StartReceptionSessionCommand) (StartReceptionSessionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateCommon(command.SessionID, command.AccessContextRef, command.IdempotencyKey); err != nil {
		return StartReceptionSessionResult{}, err
	}
	fingerprint := fingerprint(command)
	if replay, err := s.replay(ctx, "start", command.IdempotencyKey, fingerprint); replay != nil || err != nil {
		if err != nil {
			return StartReceptionSessionResult{}, err
		}
		return replay.Result.(StartReceptionSessionResult), nil
	}
	session, err := s.store.GetSession(ctx, command.SessionID)
	if err != nil {
		return StartReceptionSessionResult{}, normalizeError(err)
	}
	if _, err := s.authorizeForSession(ctx, command.AccessContextRef, "reception:start", session); err != nil {
		return StartReceptionSessionResult{}, err
	}
	if session.Version != command.ExpectedVersion {
		return StartReceptionSessionResult{}, versionError()
	}
	if session.Status != ReceptionSessionCreated {
		return StartReceptionSessionResult{}, businessError(CodeInvalidSessionState, "只有 created 会话可以启动。")
	}
	if _, err := s.configs.GetPublishedConfig(ctx, PublishedConfigRequest{
		OrganizationID: session.OrganizationID, ServicePointID: session.ServicePointID,
		ServiceWindowID: session.ServiceWindowID, ExpectedVersion: session.OrganizationConfigVersion,
	}); err != nil {
		return StartReceptionSessionResult{}, normalizeError(err)
	}
	processing, err := s.processingContext(ctx, session.ProcessingContextRef)
	if err != nil {
		return StartReceptionSessionResult{}, err
	}
	if !processing.ReceptionAllowed {
		return StartReceptionSessionResult{}, businessError(CodeReceptionNotAllowed, "当前处理上下文不允许开始接待。")
	}
	now := s.clock.Now()
	session.Status = ReceptionSessionActive
	session.Version++
	session.StartedAt = &now
	result := StartReceptionSessionResult{Session: sessionView(session, nil, capability(processing)), MediaCapability: capability(processing)}
	mutation := s.mutation("start", command.IdempotencyKey, fingerprint, result, session, nil, EventReceptionSessionStarted, "reception:start", now)
	if err := s.store.Commit(ctx, mutation); err != nil {
		return StartReceptionSessionResult{}, normalizeError(err)
	}
	return result, nil
}

func (s *Service) AttachFakeMedia(ctx context.Context, command AttachFakeMediaTrackCommand) (AttachFakeMediaResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateAttach(command); err != nil {
		return AttachFakeMediaResult{}, err
	}
	fingerprint := fingerprint(command)
	if replay, err := s.replay(ctx, "attach", command.IdempotencyKey, fingerprint); replay != nil || err != nil {
		if err != nil {
			return AttachFakeMediaResult{}, err
		}
		result := replay.Result.(AttachFakeMediaResult)
		if result.Degradation != nil {
			return result, dependencyError(CodeMediaAttachFailed, "Fake Media 接入失败，已切换到人工文本。", nil)
		}
		return result, nil
	}
	session, err := s.store.GetSession(ctx, command.SessionID)
	if err != nil {
		return AttachFakeMediaResult{}, normalizeError(err)
	}
	if _, err := s.authorizeForSession(ctx, command.AccessContextRef, "reception:attach_media", session); err != nil {
		return AttachFakeMediaResult{}, err
	}
	if session.Version != command.ExpectedSessionVersion {
		return AttachFakeMediaResult{}, versionError()
	}
	if session.Status != ReceptionSessionActive {
		return AttachFakeMediaResult{}, businessError(CodeInvalidSessionState, "只有 active 会话可以接入媒体。")
	}
	processing, err := s.processingContext(ctx, session.ProcessingContextRef)
	if err != nil {
		return AttachFakeMediaResult{}, err
	}
	if !processing.RealtimeAudioAllowed {
		return AttachFakeMediaResult{}, businessError(CodeRealtimeAudioNotAllowed, "当前处理上下文不允许实时音频。")
	}
	active, err := s.store.FindActiveBinding(ctx, session.SessionID)
	if err != nil {
		return AttachFakeMediaResult{}, normalizeError(err)
	}
	if active != nil {
		return AttachFakeMediaResult{}, businessError(CodeActiveMediaBindingExists, "当前会话已存在活动媒体绑定。")
	}
	now := s.clock.Now()
	binding := MediaTrackBinding{
		BindingID: s.ids.NewID("binding"), SessionID: session.SessionID, TrackRef: command.TrackRef,
		TrackKind: "fake", SourceType: "synthetic_audio", Provider: "fake", Scenario: command.Scenario,
		Status: MediaTrackPending, Version: 1,
	}
	_, attachErr := s.media.Attach(ctx, AttachMediaRequest{
		SessionID: session.SessionID, BindingID: binding.BindingID, TrackRef: binding.TrackRef, Scenario: binding.Scenario,
	})
	eventType := EventMediaTrackAttached
	result := AttachFakeMediaResult{}
	if attachErr != nil {
		binding.Status = MediaTrackFailed
		binding.Version++
		binding.FailedAt = &now
		binding.FailureCode = CodeMediaAttachFailed
		eventType = EventMediaTrackUnavailable
		result.Degradation = &DegradationView{Mode: "manual_text", SessionRemainsActive: true, ReasonCode: CodeMediaAttachFailed}
	} else {
		binding.Status = MediaTrackAttached
		binding.Version++
		binding.AttachedAt = &now
	}
	result.Binding = bindingView(binding)
	mutation := s.mutation("attach", command.IdempotencyKey, fingerprint, result, session, &binding, eventType, "reception:attach_media", now)
	if attachErr != nil {
		mutation.Audits[0].Result = "degraded"
	}
	if err := s.store.Commit(ctx, mutation); err != nil {
		return AttachFakeMediaResult{}, normalizeError(err)
	}
	if attachErr != nil {
		return result, dependencyError(CodeMediaAttachFailed, "Fake Media 接入失败，已切换到人工文本。", attachErr)
	}
	return result, nil
}

func (s *Service) DetachMedia(ctx context.Context, command DetachMediaTrackCommand) (MediaTrackBindingView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateDetach(command); err != nil {
		return MediaTrackBindingView{}, err
	}
	fingerprint := fingerprint(command)
	if replay, err := s.replay(ctx, "detach", command.IdempotencyKey, fingerprint); replay != nil || err != nil {
		if err != nil {
			return MediaTrackBindingView{}, err
		}
		return replay.Result.(MediaTrackBindingView), nil
	}
	session, err := s.store.GetSession(ctx, command.SessionID)
	if err != nil {
		return MediaTrackBindingView{}, normalizeError(err)
	}
	if _, err := s.authorizeForSession(ctx, command.AccessContextRef, "reception:detach_media", session); err != nil {
		return MediaTrackBindingView{}, err
	}
	binding, err := s.store.GetBinding(ctx, command.BindingID)
	if err != nil {
		return MediaTrackBindingView{}, normalizeError(err)
	}
	if binding.SessionID != session.SessionID {
		return MediaTrackBindingView{}, businessError(CodeBindingNotFound, "媒体绑定不属于当前会话。")
	}
	if binding.Version != command.ExpectedBindingVersion {
		return MediaTrackBindingView{}, versionError()
	}
	if binding.Status != MediaTrackAttached {
		return MediaTrackBindingView{}, businessError(CodeInvalidBindingState, "只有 attached 媒体绑定可以断开。")
	}
	if err := s.media.Detach(ctx, DetachMediaRequest{SessionID: session.SessionID, BindingID: binding.BindingID, TrackRef: binding.TrackRef, Scenario: binding.Scenario}); err != nil {
		return MediaTrackBindingView{}, dependencyError(CodeMediaDetachFailed, "Fake Media 断开失败，会话仍保持活动。", err)
	}
	if err := s.cleaner.Clean(ctx, binding.BindingID); err != nil {
		return MediaTrackBindingView{}, dependencyError(CodeMediaDetachFailed, "媒体资源清理失败，会话仍保持活动。", err)
	}
	now := s.clock.Now()
	binding.Status = MediaTrackDetached
	binding.Version++
	binding.DetachedAt = &now
	view := bindingView(binding)
	mutation := s.mutation("detach", command.IdempotencyKey, fingerprint, view, session, &binding, EventMediaTrackDetached, "reception:detach_media", now)
	if err := s.store.Commit(ctx, mutation); err != nil {
		return MediaTrackBindingView{}, normalizeError(err)
	}
	return view, nil
}

func (s *Service) HandleRuntimeDisconnect(ctx context.Context, bindingID string) (AttachFakeMediaResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, err := s.store.GetBinding(ctx, bindingID)
	if err != nil {
		return AttachFakeMediaResult{}, normalizeError(err)
	}
	if binding.Status != MediaTrackAttached || binding.Scenario != FakeScenarioRuntimeDisconnect {
		return AttachFakeMediaResult{}, businessError(CodeInvalidBindingState, "该媒体绑定不能触发运行中断线。")
	}
	session, err := s.store.GetSession(ctx, binding.SessionID)
	if err != nil {
		return AttachFakeMediaResult{}, normalizeError(err)
	}
	if err := s.cleaner.Clean(ctx, binding.BindingID); err != nil {
		return AttachFakeMediaResult{}, dependencyError(CodeInternalError, "媒体资源清理失败。", err)
	}
	now := s.clock.Now()
	binding.Status = MediaTrackFailed
	binding.Version++
	binding.FailedAt = &now
	binding.FailureCode = "MEDIA_RUNTIME_DISCONNECTED"
	result := AttachFakeMediaResult{
		Binding:     bindingView(binding),
		Degradation: &DegradationView{Mode: "manual_text", SessionRemainsActive: true, ReasonCode: "MEDIA_RUNTIME_DISCONNECTED"},
	}
	mutation := s.mutation("", "", "", nil, session, &binding, EventMediaTrackUnavailable, "reception:runtime_disconnect", now)
	if err := s.store.Commit(ctx, mutation); err != nil {
		return AttachFakeMediaResult{}, normalizeError(err)
	}
	return result, nil
}

func (s *Service) EndSession(ctx context.Context, command EndReceptionSessionCommand) (ReceptionSessionView, error) {
	return s.finishSession(ctx, "end", command.SessionID, command.AccessContextRef, command.IdempotencyKey,
		command.ExpectedVersion, "", ReceptionSessionEnded)
}

func (s *Service) CancelSession(ctx context.Context, command CancelReceptionSessionCommand) (ReceptionSessionView, error) {
	if command.ReasonCode == "" {
		return ReceptionSessionView{}, businessError(CodeValidationFailed, "reason_code 为必填字段。")
	}
	return s.finishSession(ctx, "cancel", command.SessionID, command.AccessContextRef, command.IdempotencyKey,
		command.ExpectedVersion, command.ReasonCode, ReceptionSessionCancelled)
}

func (s *Service) finishSession(ctx context.Context, operation, sessionID, accessRef, key string, expected int64, reason string, target ReceptionSessionStatus) (ReceptionSessionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateCommon(sessionID, accessRef, key); err != nil {
		return ReceptionSessionView{}, err
	}
	fingerprint := fingerprint(struct {
		SessionID, AccessRef, Key, Reason string
		Expected                          int64
	}{sessionID, accessRef, key, reason, expected})
	if replay, err := s.replay(ctx, operation, key, fingerprint); replay != nil || err != nil {
		if err != nil {
			return ReceptionSessionView{}, err
		}
		return replay.Result.(ReceptionSessionView), nil
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	if _, err := s.authorizeForSession(ctx, accessRef, "reception:"+operation, session); err != nil {
		return ReceptionSessionView{}, err
	}
	if session.Version != expected {
		return ReceptionSessionView{}, versionError()
	}
	if target == ReceptionSessionEnded && session.Status != ReceptionSessionActive {
		return ReceptionSessionView{}, businessError(CodeInvalidSessionState, "只有 active 会话可以正常结束。")
	}
	if target == ReceptionSessionCancelled && session.Status != ReceptionSessionCreated && session.Status != ReceptionSessionActive {
		return ReceptionSessionView{}, businessError(CodeInvalidSessionState, "只有 created 或 active 会话可以取消。")
	}
	active, err := s.store.FindActiveBinding(ctx, sessionID)
	if err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	var updatedBinding *MediaTrackBinding
	if active != nil {
		_ = s.media.Detach(ctx, DetachMediaRequest{SessionID: sessionID, BindingID: active.BindingID, TrackRef: active.TrackRef, Scenario: active.Scenario})
		_ = s.cleaner.Clean(ctx, active.BindingID)
		now := s.clock.Now()
		active.Status = MediaTrackFailed
		active.Version++
		active.FailedAt = &now
		active.FailureCode = "SESSION_CLOSED"
		updatedBinding = active
	}
	now := s.clock.Now()
	session.Status = target
	session.Version++
	eventType := EventReceptionSessionEnded
	if target == ReceptionSessionEnded {
		session.EndedAt = &now
	} else {
		session.CancelledAt = &now
		session.CancellationReasonCode = reason
		eventType = EventReceptionSessionCancelled
	}
	bindings, _ := s.store.BindingsBySession(ctx, sessionID)
	if updatedBinding != nil {
		for index := range bindings {
			if bindings[index].BindingID == updatedBinding.BindingID {
				bindings[index] = *updatedBinding
			}
		}
	}
	processing, _ := s.processing.GetProcessingContext(ctx, session.ProcessingContextRef)
	view := sessionView(session, bindings, capability(processing))
	mutation := s.mutation(operation, key, fingerprint, view, session, updatedBinding, eventType, "reception:"+operation, now)
	if updatedBinding != nil {
		mutation.Events = append([]DomainEvent{s.domainEvent(session, updatedBinding, EventMediaTrackUnavailable, now)}, mutation.Events...)
	}
	if err := s.store.Commit(ctx, mutation); err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	return view, nil
}

func (s *Service) authorize(ctx context.Context, ref, action, org, point, window string) (AccessContextView, error) {
	view, err := s.authorizer.Authorize(ctx, AuthorizeRequest{AccessContextRef: ref, Action: action, OrganizationID: org, ServicePointID: point, ServiceWindowID: window})
	if err != nil {
		return AccessContextView{}, normalizeError(err)
	}
	return view, nil
}

func (s *Service) authorizeForSession(ctx context.Context, ref, action string, session ReceptionSession) (AccessContextView, error) {
	return s.authorize(ctx, ref, action, session.OrganizationID, session.ServicePointID, session.ServiceWindowID)
}

func (s *Service) processingContext(ctx context.Context, ref string) (ProcessingContextView, error) {
	view, err := s.processing.GetProcessingContext(ctx, ref)
	if err != nil {
		return ProcessingContextView{}, normalizeError(err)
	}
	if view.Expired {
		return ProcessingContextView{}, businessError(CodeProcessingContextExpired, "处理上下文已过期。")
	}
	return view, nil
}

func (s *Service) replay(ctx context.Context, operation, key, wantFingerprint string) (*IdempotencyRecord, error) {
	record, err := s.store.Replay(ctx, operation, key)
	if err != nil {
		return nil, normalizeError(err)
	}
	if record != nil && record.Fingerprint != wantFingerprint {
		return nil, businessError(CodeIdempotencyConflict, "幂等键已用于不同请求。")
	}
	return record, nil
}

func (s *Service) mutation(operation, key, requestFingerprint string, result any, session ReceptionSession, binding *MediaTrackBinding, eventType, action string, now time.Time) Mutation {
	eventBinding := binding
	if !strings.HasPrefix(eventType, "Media") {
		eventBinding = nil
	}
	event := s.domainEvent(session, eventBinding, eventType, now)
	aggregateID := event.AggregateID
	audit := AuditEntry{
		AuditID: s.ids.NewID("audit"), Action: action, AggregateID: aggregateID, SessionID: session.SessionID,
		OperatorID: session.OperatorID, OrganizationID: session.OrganizationID, OccurredAt: now, Result: "success",
	}
	mutation := Mutation{Session: &session, Binding: binding, Events: []DomainEvent{event}, Audits: []AuditEntry{audit}}
	if operation != "" {
		mutation.Idempotency = &IdempotencyRecord{Operation: operation, Key: key, Fingerprint: requestFingerprint, Result: result}
	}
	return mutation
}

func (s *Service) domainEvent(session ReceptionSession, binding *MediaTrackBinding, eventType string, now time.Time) DomainEvent {
	aggregateID := session.SessionID
	aggregateType := "ReceptionSession"
	aggregateVersion := session.Version
	payload := map[string]any{"status": session.Status, "organization_config_version": session.OrganizationConfigVersion}
	if binding != nil {
		aggregateID = binding.BindingID
		aggregateType = "MediaTrackBinding"
		aggregateVersion = binding.Version
		payload = map[string]any{
			"binding_id": binding.BindingID, "track_ref": binding.TrackRef, "status": binding.Status,
			"failure_code": binding.FailureCode, "organization_config_version": session.OrganizationConfigVersion,
		}
	}
	return DomainEvent{
		EventID: s.ids.NewID("event"), EventType: eventType, AggregateID: aggregateID,
		AggregateType: aggregateType, AggregateVersion: aggregateVersion, SessionID: session.SessionID,
		OrganizationID: session.OrganizationID, OperatorID: session.OperatorID,
		TraceID: s.ids.NewID("trace"), OccurredAt: now, Payload: payload,
	}
}

func capability(view ProcessingContextView) MediaCapability {
	return MediaCapability{AudioTrackAllowed: view.RealtimeAudioAllowed, ManualTextAvailable: true}
}

func fingerprint(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validateCreate(command CreateReceptionSessionCommand) error {
	if command.IdempotencyKey == "" || command.AccessContextRef == "" || command.OrganizationID == "" ||
		command.ServicePointID == "" || command.ServiceWindowID == "" || command.OrganizationConfigVersion == "" ||
		command.ProcessingContextRef == "" {
		return businessError(CodeValidationFailed, "创建会话请求缺少必填字段。")
	}
	return nil
}

func validateCommon(sessionID, accessRef, key string) error {
	if sessionID == "" || accessRef == "" || key == "" {
		return businessError(CodeValidationFailed, "请求缺少必填字段。")
	}
	return nil
}

func validateAttach(command AttachFakeMediaTrackCommand) error {
	if err := validateCommon(command.SessionID, command.AccessContextRef, command.IdempotencyKey); err != nil {
		return err
	}
	if command.TrackRef == "" {
		return businessError(CodeValidationFailed, "track_ref 为必填字段。")
	}
	switch command.Scenario {
	case FakeScenarioSuccess, FakeScenarioAttachFailure, FakeScenarioRuntimeDisconnect, FakeScenarioDetachFailure:
		return nil
	default:
		return businessError(CodeUnsupportedFakeScenario, "不支持的 Fake Media 场景。")
	}
}

func validateDetach(command DetachMediaTrackCommand) error {
	if err := validateCommon(command.SessionID, command.AccessContextRef, command.IdempotencyKey); err != nil {
		return err
	}
	if command.BindingID == "" {
		return businessError(CodeValidationFailed, "binding_id 为必填字段。")
	}
	return nil
}

func versionError() error {
	return businessError(CodeVersionMismatch, "数据已被修改，请刷新后重试。")
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var business *Error
	if errors.As(err, &business) {
		return business
	}
	return &Error{Code: CodeInternalError, Message: "服务暂时不可用。", Details: map[string]any{}, Cause: err}
}
