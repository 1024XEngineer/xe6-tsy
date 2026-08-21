package delivery

import (
	"context"
	"errors"
	"testing"
)

type channelProviderStub struct {
	channel Channel
	called  bool
}

func (s *channelProviderStub) Send(_ context.Context, request SendRequest) error {
	s.called = true
	if request.Message.Channel != s.channel {
		return ErrProviderRejected
	}
	return nil
}

func (s *channelProviderStub) SupportsProviderIdempotency() bool { return false }

func TestChannelRouterDelegatesByChannel(t *testing.T) {
	email := &channelProviderStub{channel: ChannelEmail}
	wechat := &channelProviderStub{channel: ChannelWeChat}
	router := NewChannelRouter(email, wechat)

	if err := router.Send(t.Context(), SendRequest{Message: Message{Channel: ChannelEmail}}); err != nil {
		t.Fatalf("Send(email) error = %v", err)
	}
	if !email.called || wechat.called {
		t.Fatalf("router calls = (%v, %v), want (true, false)", email.called, wechat.called)
	}

	wechat.called = false
	if err := router.Send(t.Context(), SendRequest{Message: Message{Channel: ChannelWeChat}}); err != nil {
		t.Fatalf("Send(wechat) error = %v", err)
	}
	if !wechat.called {
		t.Fatal("wechat provider was not called")
	}
}

func TestChannelRouterReturnsNotConfiguredForMissingProvider(t *testing.T) {
	router := NewChannelRouter(NewFakeEmailProvider(FakeEmailProviderConfig{}), UnconfiguredProvider{})
	err := router.Send(t.Context(), SendRequest{Message: Message{Channel: ChannelWeChat}})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("Send() error = %v, want provider not configured", err)
	}
	if !router.SupportsChannel(ChannelEmail) {
		t.Fatal("SupportsChannel(email) = false, want true")
	}
	if router.SupportsChannel(ChannelWeChat) {
		t.Fatal("SupportsChannel(wechat) = true, want false")
	}
}
