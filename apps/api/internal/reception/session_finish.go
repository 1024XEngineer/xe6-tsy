package reception

import "context"

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
		if err := s.detachBeforeSessionClose(ctx, *active); err != nil {
			return ReceptionSessionView{}, err
		}
		updatedBinding = active
	}
	now := s.clock.Now()
	if updatedBinding != nil {
		updatedBinding.Status = MediaTrackDetached
		updatedBinding.Version++
		updatedBinding.DetachedAt = &now
	}
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
		mutation.Events = append([]DomainEvent{s.domainEvent(session, updatedBinding, EventMediaTrackDetached, now)}, mutation.Events...)
	}
	if err := s.store.Commit(ctx, mutation); err != nil {
		return ReceptionSessionView{}, normalizeError(err)
	}
	return view, nil
}

// detachBeforeSessionClose keeps the persisted session and binding retryable until both
// external cleanup steps succeed. Detach must be idempotent because a later store commit
// can fail after the adapter has already released its resource.
func (s *Service) detachBeforeSessionClose(ctx context.Context, binding MediaTrackBinding) error {
	request := DetachMediaRequest{
		SessionID: binding.SessionID, BindingID: binding.BindingID,
		TrackRef: binding.TrackRef, Scenario: binding.Scenario,
	}
	if err := s.media.Detach(ctx, request); err != nil {
		return dependencyError(CodeMediaDetachFailed, "Fake Media 断开失败，会话仍保持活动。", err)
	}
	if err := s.cleaner.Clean(ctx, binding.BindingID); err != nil {
		return dependencyError(CodeMediaDetachFailed, "媒体资源清理失败，会话仍保持活动。", err)
	}
	return nil
}
