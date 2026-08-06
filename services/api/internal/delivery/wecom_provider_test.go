package delivery

import (
	"context"
	"strings"
	"testing"
)

type weComMessengerStub struct {
	userid  string
	content string
}

func (s *weComMessengerStub) SendTextMessage(_ context.Context, userid, content string) error {
	s.userid = userid
	s.content = content
	return nil
}

func TestWeComProviderSendDeliversSnapshot(t *testing.T) {
	messenger := &weComMessengerStub{}
	provider, err := NewWeComProvider(messenger)
	if err != nil {
		t.Fatalf("NewWeComProvider() error = %v", err)
	}
	request := validWeChatFakeRequest()
	if err := provider.Send(t.Context(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if messenger.userid != "userid-1" || !strings.Contains(messenger.content, "Turn: turn-1") {
		t.Fatalf("messenger = (%q, %q)", messenger.userid, messenger.content)
	}
}

func validWeChatFakeRequest() SendRequest {
	request := validFakeRequest()
	request.Message.Channel = ChannelWeChat
	request.Destination.Channel = ChannelWeChat
	request.Destination.ProviderTarget = "userid-1"
	return request
}
