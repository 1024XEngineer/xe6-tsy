package reception

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

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
