package delivery

import (
	"context"
	"fmt"
	"strings"
)

// WeComProvider sends outbound WeChat Work application messages.
type WeComProvider struct {
	client WeComMessenger
}

func NewWeComProvider(client WeComMessenger) (*WeComProvider, error) {
	if client == nil {
		return nil, fmt.Errorf("wecom messenger is required")
	}
	return &WeComProvider{client: client}, nil
}

func (p *WeComProvider) Send(ctx context.Context, request SendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWeChatDeliveryRequest(request); err != nil {
		return err
	}
	return p.client.SendTextMessage(ctx, request.Destination.ProviderTarget, formatWeChatDeliveryBody(request))
}

func (p *WeComProvider) SupportsProviderIdempotency() bool {
	return false
}

func validateWeChatDeliveryRequest(request SendRequest) error {
	if strings.TrimSpace(request.ProviderIdempotencyKey) == "" ||
		request.Message.ID == "" ||
		request.Message.AccountID == "" ||
		request.ProviderIdempotencyKey != request.Attempt.ID ||
		request.Attempt.MessageID != request.Message.ID ||
		request.Message.Channel != ChannelWeChat {
		return domainErrInvalidDeliveryRequest()
	}
	userid, err := validateWeComUserID(request.Destination.ProviderTarget)
	if err != nil {
		return err
	}
	if request.Destination.AccountID != request.Message.AccountID ||
		request.Destination.Channel != ChannelWeChat ||
		request.Destination.DestinationRef != request.Message.DestinationRef ||
		userid != request.Destination.ProviderTarget {
		return domainErrInvalidDeliveryRequest()
	}
	return nil
}

func formatWeChatDeliveryBody(request SendRequest) string {
	return "Your Lingow transcript delivery:\n\n" + formatDeliveryTurns(request.Message.Turns)
}

func domainErrInvalidDeliveryRequest() error {
	return fmt.Errorf("%w: invalid delivery request", ErrProviderRejected)
}

var _ Provider = (*WeComProvider)(nil)
var _ IdempotentProvider = (*WeComProvider)(nil)
