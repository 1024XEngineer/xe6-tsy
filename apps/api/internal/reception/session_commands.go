package reception

import "context"

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
