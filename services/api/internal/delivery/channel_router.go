package delivery

import (
	"context"
	"fmt"
)

// ChannelRouter delegates outbound delivery to a channel-specific provider.
type ChannelRouter struct {
	email   Provider
	wechat  Provider
	webhook Provider
}

func NewChannelRouter(emailProvider, wechatProvider Provider, webhookProvider ...Provider) *ChannelRouter {
	var webhook Provider
	if len(webhookProvider) > 0 {
		webhook = webhookProvider[0]
	}
	return &ChannelRouter{email: emailProvider, wechat: wechatProvider, webhook: webhook}
}

func (r *ChannelRouter) Send(ctx context.Context, request SendRequest) error {
	if r == nil {
		return fmt.Errorf("%w: channel router is not configured", ErrProviderNotConfigured)
	}
	switch request.Message.Channel {
	case ChannelEmail:
		return r.send(ctx, r.email, request)
	case ChannelWeChat:
		return r.send(ctx, r.wechat, request)
	case ChannelWebhook:
		return r.send(ctx, r.webhook, request)
	default:
		return fmt.Errorf("%w: unsupported channel %q", ErrProviderNotConfigured, request.Message.Channel)
	}
}

// SupportsChannel reports whether the router has a configured provider for a
// channel. It is a composition-time capability check; provider health remains
// handled by the delivery worker's retry and failure state machine.
func (r *ChannelRouter) SupportsChannel(channel Channel) bool {
	if r == nil {
		return false
	}
	switch channel {
	case ChannelEmail:
		return providerConfigured(r.email)
	case ChannelWeChat:
		return providerConfigured(r.wechat)
	case ChannelWebhook:
		return providerConfigured(r.webhook)
	default:
		return false
	}
}

func (r *ChannelRouter) send(ctx context.Context, provider Provider, request SendRequest) error {
	if provider == nil {
		return fmt.Errorf("%w: provider adapter is not wired", ErrProviderNotConfigured)
	}
	return provider.Send(ctx, request)
}

func providerConfigured(provider Provider) bool {
	switch provider.(type) {
	case nil, UnconfiguredProvider, *UnconfiguredProvider:
		return false
	default:
		return true
	}
}

func (r *ChannelRouter) SupportsProviderIdempotency() bool {
	return false
}

var _ Provider = (*ChannelRouter)(nil)
var _ IdempotentProvider = (*ChannelRouter)(nil)
