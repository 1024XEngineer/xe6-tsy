package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type weComIdentityStub struct {
	userid string
	err    error
}

func (s *weComIdentityStub) UserIDFromOAuthCode(_ context.Context, code string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if code != "oauth-code-1" {
		return "", domain.ErrUnauthorized
	}
	return s.userid, nil
}

func TestResolveWeChatBindCodeUsesOAuthClient(t *testing.T) {
	ref, userid, err := resolveWeChatBindCode(t.Context(), "production", "oauth-code-1", &weComIdentityStub{userid: "userid-1"})
	if err != nil {
		t.Fatalf("resolveWeChatBindCode() error = %v", err)
	}
	if ref != "primary-wechat" || userid != "userid-1" {
		t.Fatalf("resolveWeChatBindCode() = (%q, %q)", ref, userid)
	}
}

func TestParseDevWeChatBindCodeRejectsInjection(t *testing.T) {
	_, _, err := parseDevWeChatBindCode("local", "dev:user\r\nid")
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("parseDevWeChatBindCode() error = %v, want invalid argument", err)
	}
}

func TestBindWeChatTargetWithOAuthClient(t *testing.T) {
	repository := &targetRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	service.ConfigureWeChatBinding(&weComIdentityStub{userid: "userid-1"})

	target, err := service.BindWeChatTarget(t.Context(), "account-1", "oauth-code-1")
	if err != nil {
		t.Fatalf("BindWeChatTarget() error = %v", err)
	}
	if target.Channel != ChannelWeChat || repository.bindWeChatRecord.AccountID != "account-1" {
		t.Fatalf("BindWeChatTarget() = %#v record = %#v", target, repository.bindWeChatRecord)
	}
}
