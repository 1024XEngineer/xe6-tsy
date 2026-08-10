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
	if messenger.userid != "userid-1" {
		t.Fatalf("messenger = (%q, %q)", messenger.userid, messenger.content)
	}
	for _, want := range []string{"Turn: turn-1", anonymousSpeakerLabel, "hello", "你好"} {
		if !strings.Contains(messenger.content, want) {
			t.Fatalf("content = %q, want substring %q", messenger.content, want)
		}
	}
	if strings.Contains(messenger.content, "Personal name") {
		t.Fatalf("content = %q, must not include participant label", messenger.content)
	}
}

func validWeChatFakeRequest() SendRequest {
	request := validFakeRequest()
	speakerLabel := "Personal name"
	request.Message.Channel = ChannelWeChat
	request.Message.Turns[0].SpeakerLabelSnapshot = &speakerLabel
	request.Message.Turns[0].SourceText = "hello"
	request.Message.Turns[0].TranslatedText = "你好"
	request.Destination.Channel = ChannelWeChat
	request.Destination.ProviderTarget = "userid-1"
	return request
}
