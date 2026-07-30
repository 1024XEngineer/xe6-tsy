package delivery

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const defaultWeChatDestinationRef = "primary-wechat"

// WeComIdentityClient resolves a WeChat Work OAuth code to a stable userid.
type WeComIdentityClient interface {
	UserIDFromOAuthCode(context.Context, string) (string, error)
}

// WeComMessenger sends outbound WeChat Work application messages.
type WeComMessenger interface {
	SendTextMessage(context.Context, string, string) error
}

// BindWeChatTargetRecord persists one verified WeChat Work destination for an account.
type BindWeChatTargetRecord struct {
	ID             string
	AccountID      string
	DestinationRef string
	Ciphertext     []byte
	KeyVersion     string
	VerifiedAt     time.Time
}

// parseDevWeChatBindCode accepts local-only bind codes documented on BindWeChatRequest.
// Formats: dev:<userid> or dev:<destination_ref>:<userid>.
func parseDevWeChatBindCode(appEnv, code string) (destinationRef, userid string, err error) {
	if code == "" {
		return "", "", domain.ErrInvalidArgument
	}
	if !strings.HasPrefix(code, "dev:") {
		return "", "", domain.ErrNotImplemented
	}
	if !AllowsDevEmailBindEnvironment(appEnv) {
		return "", "", domain.ErrNotImplemented
	}
	payload := strings.TrimPrefix(code, "dev:")
	switch parts := strings.SplitN(payload, ":", 2); len(parts) {
	case 1:
		userid = strings.TrimSpace(parts[0])
		destinationRef = defaultWeChatDestinationRef
	case 2:
		destinationRef = strings.TrimSpace(parts[0])
		userid = strings.TrimSpace(parts[1])
	default:
		return "", "", domain.ErrInvalidArgument
	}
	if destinationRef == "" {
		return "", "", domain.ErrInvalidArgument
	}
	userid, err = validateWeComUserID(userid)
	if err != nil {
		return "", "", err
	}
	return destinationRef, userid, nil
}

func resolveWeChatBindCode(ctx context.Context, appEnv, code string, client WeComIdentityClient) (destinationRef, userid string, err error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", "", domain.ErrInvalidArgument
	}
	if strings.HasPrefix(code, "dev:") {
		return parseDevWeChatBindCode(appEnv, code)
	}
	if client == nil {
		return "", "", domain.ErrNotImplemented
	}
	userid, err = client.UserIDFromOAuthCode(ctx, code)
	if err != nil {
		return "", "", err
	}
	return defaultWeChatDestinationRef, userid, nil
}

func validateWeComUserID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", domain.ErrInvalidArgument
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", domain.ErrInvalidArgument
		}
		if unicode.IsSpace(r) {
			return "", domain.ErrInvalidArgument
		}
	}
	if strings.ContainsAny(raw, "<>\"@\\") {
		return "", domain.ErrInvalidArgument
	}
	return raw, nil
}
