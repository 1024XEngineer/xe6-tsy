package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// WeCom OAuth and API errcode references:
// https://developer.work.weixin.qq.com/document/path/90313
const (
	weComErrInvalidCredential  = 40014
	weComErrAccessTokenExpired = 42001
	weComErrInvalidOAuthCode   = 40029
)

func sanitizeWeComTransportError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("wecom transport failed")
}

func mapWeComOAuthError(errCode int, errMsg string) error {
	switch errCode {
	case weComErrInvalidOAuthCode, 40063, 40163:
		return domain.ErrInvalidArgument
	default:
		return fmt.Errorf("wecom getuserinfo: %s (code %d)", errMsg, errCode)
	}
}

func mapWeComSendError(errCode int, errMsg string) error {
	switch errCode {
	case weComErrInvalidCredential, weComErrAccessTokenExpired:
		return fmt.Errorf("wecom message send: token refresh required (code %d)", errCode)
	default:
		return fmt.Errorf("%w: wecom message send: %s (code %d)", ErrProviderRejected, errMsg, errCode)
	}
}

func isWeComTokenRefreshError(errCode int) bool {
	return errCode == weComErrInvalidCredential || errCode == weComErrAccessTokenExpired
}
