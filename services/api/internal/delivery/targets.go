package delivery

import (
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const destinationKeyVersion = "v1"

// MessageTarget is the account-visible binding metadata. Provider targets never
// appear in this shape; only destination_ref and verification state are exposed.
type MessageTarget struct {
	DestinationRef string     `json:"destination_ref"`
	Channel        Channel    `json:"channel"`
	Verified       bool       `json:"verified"`
	RevokedAt      *time.Time `json:"revoked_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// BindEmailTargetRecord persists one verified email destination for an account.
type BindEmailTargetRecord struct {
	ID             string
	AccountID      string
	DestinationRef string
	Ciphertext     []byte
	KeyVersion     string
	VerifiedAt     time.Time
}

// parseDevEmailBindToken accepts local-only bind tokens documented on BindEmailRequest.
// Formats: dev:<email> or dev:<destination_ref>:<email>.
func parseDevEmailBindToken(appEnv, token string) (destinationRef, email string, err error) {
	if token == "" {
		return "", "", domain.ErrInvalidArgument
	}
	if !strings.HasPrefix(token, "dev:") {
		return "", "", domain.ErrNotImplemented
	}
	if !allowsDevEmailBindToken(appEnv) {
		return "", "", domain.ErrNotImplemented
	}
	payload := strings.TrimPrefix(token, "dev:")
	switch parts := strings.SplitN(payload, ":", 2); len(parts) {
	case 1:
		email = strings.TrimSpace(parts[0])
		destinationRef = "primary-email"
	case 2:
		destinationRef = strings.TrimSpace(parts[0])
		email = strings.TrimSpace(parts[1])
	default:
		return "", "", domain.ErrInvalidArgument
	}
	if destinationRef == "" || email == "" || !strings.Contains(email, "@") {
		return "", "", domain.ErrInvalidArgument
	}
	return destinationRef, email, nil
}

func allowsDevEmailBindToken(appEnv string) bool {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "local", "test":
		return true
	default:
		return false
	}
}
