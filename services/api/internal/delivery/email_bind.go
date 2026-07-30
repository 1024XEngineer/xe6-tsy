package delivery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/oklog/ulid/v2"
)

const emailBindChallengeTTL = 15 * time.Minute

// EmailBindChallenge is a short-lived ownership proof before persisting a destination.
type EmailBindChallenge struct {
	ID             string
	AccountID      string
	DestinationRef string
	Email          string
	TokenHash      string
	ExpiresAt      time.Time
	UsedAt         *time.Time
	CreatedAt      time.Time
}

// EmailBindChallengeRepository stores one-time email bind tokens durably.
type EmailBindChallengeRepository interface {
	CreateEmailBindChallenge(context.Context, EmailBindChallenge) error
	ConsumeEmailBindChallenge(context.Context, string, string) (EmailBindChallenge, error)
}

// EmailBindSender delivers one-time bind tokens to an inbox without logging the secret.
type EmailBindSender interface {
	SendBindToken(context.Context, string, string, string) error
}

// LogEmailBindSender records bind tokens in structured logs for local development.
type LogEmailBindSender struct{}

func (LogEmailBindSender) SendBindToken(_ context.Context, email, destinationRef, token string) error {
	if strings.TrimSpace(email) == "" || strings.TrimSpace(token) == "" {
		return domain.ErrInvalidArgument
	}
	fmt.Printf("email bind token destination_ref=%s email=%s token=%s\n", destinationRef, email, token)
	return nil
}

func hashEmailBindToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func generateEmailBindToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate email bind token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func normalizeBindEmail(email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return "", domain.ErrInvalidArgument
	}
	return email, nil
}

func normalizeBindDestinationRef(destinationRef string) string {
	destinationRef = strings.TrimSpace(destinationRef)
	if destinationRef == "" {
		return "primary-email"
	}
	return destinationRef
}

// resolveEmailBindToken accepts local dev tokens or a consumed verification token.
func resolveEmailBindToken(
	ctx context.Context,
	appEnv, token, accountID string,
	challenges EmailBindChallengeRepository,
) (destinationRef, email string, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", domain.ErrInvalidArgument
	}
	if strings.HasPrefix(token, "dev:") {
		return parseDevEmailBindToken(appEnv, token)
	}
	if challenges == nil || accountID == "" {
		return "", "", domain.ErrNotImplemented
	}
	challenge, err := challenges.ConsumeEmailBindChallenge(ctx, accountID, hashEmailBindToken(token))
	if err != nil {
		return "", "", err
	}
	return challenge.DestinationRef, challenge.Email, nil
}

func newEmailBindChallenge(accountID, destinationRef, email, tokenHash string, now time.Time) EmailBindChallenge {
	return EmailBindChallenge{
		ID:             "email_bind_" + ulid.Make().String(),
		AccountID:      accountID,
		DestinationRef: destinationRef,
		Email:          email,
		TokenHash:      tokenHash,
		ExpiresAt:      now.Add(emailBindChallengeTTL),
		CreatedAt:      now,
	}
}
