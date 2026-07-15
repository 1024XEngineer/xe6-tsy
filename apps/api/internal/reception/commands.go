package reception

type CreateReceptionSessionCommand struct {
	IdempotencyKey            string `json:"idempotency_key"`
	AccessContextRef          string `json:"access_context_ref"`
	OrganizationID            string `json:"organization_id"`
	ServicePointID            string `json:"service_point_id"`
	ServiceWindowID           string `json:"service_window_id"`
	OrganizationConfigVersion string `json:"organization_config_version"`
	ProcessingContextRef      string `json:"processing_context_ref"`
}

type StartReceptionSessionCommand struct {
	SessionID        string `json:"session_id"`
	AccessContextRef string `json:"access_context_ref"`
	ExpectedVersion  int64  `json:"expected_version"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type AttachFakeMediaTrackCommand struct {
	SessionID              string `json:"session_id"`
	AccessContextRef       string `json:"access_context_ref"`
	ExpectedSessionVersion int64  `json:"expected_session_version"`
	IdempotencyKey         string `json:"idempotency_key"`
	TrackRef               string `json:"track_ref"`
	Scenario               string `json:"scenario"`
}

type DetachMediaTrackCommand struct {
	SessionID              string `json:"session_id"`
	BindingID              string `json:"binding_id"`
	AccessContextRef       string `json:"access_context_ref"`
	ExpectedBindingVersion int64  `json:"expected_binding_version"`
	IdempotencyKey         string `json:"idempotency_key"`
}

type EndReceptionSessionCommand struct {
	SessionID        string `json:"session_id"`
	AccessContextRef string `json:"access_context_ref"`
	ExpectedVersion  int64  `json:"expected_version"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type CancelReceptionSessionCommand struct {
	SessionID        string `json:"session_id"`
	AccessContextRef string `json:"access_context_ref"`
	ExpectedVersion  int64  `json:"expected_version"`
	IdempotencyKey   string `json:"idempotency_key"`
	ReasonCode       string `json:"reason_code"`
}
