package delivery

import (
	"context"
	"fmt"
)

// ChannelRouter delegates outbound delivery to a channel-specific provider.
type ChannelRouter struct {
	email  Provider
	wechat Provider
}

func NewChannelRouter(emailProvider, wechatProvider Provider) *ChannelRouter {
	return &ChannelRouter{email: emailProvider, wechat: wechatProvider}
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
	default:
		return fmt.Errorf("%w: unsupported channel %q", ErrProviderNotConfigured, request.Message.Channel)
	}
}

func (r *ChannelRouter) send(ctx context.Context, provider Provider, request SendRequest) error {
	if provider == nil {
		return fmt.Errorf("%w: provider adapter is not wired", ErrProviderNotConfigured)
	}
	return provider.Send(ctx, request)
}

func (r *ChannelRouter) SupportsProviderIdempotency() bool {
	return false
}

var _ Provider = (*ChannelRouter)(nil)
var _ IdempotentProvider = (*ChannelRouter)(nil)
