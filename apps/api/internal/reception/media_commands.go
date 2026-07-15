package reception

import "context"

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
