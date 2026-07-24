package delivery

import "time"

type Channel string

const (
	ChannelEmail Channel = "email"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusSending   Status = "sending"
	StatusSent      Status = "sent"
	StatusFailed    Status = "failed"
	StatusRetrying  Status = "retrying"
	StatusCancelled Status = "cancelled"
)

type TurnSnapshot struct {
	TurnID               string `json:"turn_id"`
	SpeakerLabelSnapshot string `json:"speaker_label_snapshot,omitempty"`
	SourceLanguage       string `json:"source_language"`
	TargetLanguage       string `json:"target_language"`
	SourceText           string `json:"source_text"`
	TranslatedText       string `json:"translated_text"`
}

type Message struct {
	ID              string         `json:"id"`
	AccountID       string         `json:"account_id"`
	Channel         Channel        `json:"channel"`
	DestinationRef  string         `json:"destination_ref"`
	SnapshotVersion int            `json:"snapshot_version"`
	Turns           []TurnSnapshot `json:"turns"`
	Status          Status         `json:"status"`
	Attempts        int            `json:"attempts"`
	LastErrorCode   string         `json:"last_error_code,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type CreateInput struct {
	AccountID      string   `json:"-"`
	IdempotencyKey string   `json:"-"`
	Channel        Channel  `json:"channel"`
	DestinationRef string   `json:"destination_ref"`
	TurnIDs        []string `json:"turn_ids"`
}

type Preference struct {
	AccountID string    `json:"account_id"`
	Channel   Channel   `json:"channel"`
	Enabled   bool      `json:"enabled"`
	Verified  bool      `json:"verified"`
	UpdatedAt time.Time `json:"updated_at"`
}
