package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// WebhookProvider posts a compact JSON text payload to each account's
// verified destination. The endpoint is resolved from encrypted storage by
// the worker immediately before this call.
type WebhookProvider struct {
	httpClient *http.Client
}

func NewWebhookProvider() *WebhookProvider {
	return &WebhookProvider{httpClient: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *WebhookProvider) Send(ctx context.Context, request SendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(request.ProviderIdempotencyKey) == "" ||
		request.ProviderIdempotencyKey != request.Attempt.ID ||
		request.Message.ID == "" || request.Message.AccountID == "" ||
		request.Attempt.MessageID != request.Message.ID || request.Message.Channel != ChannelWebhook ||
		request.Destination.AccountID != request.Message.AccountID ||
		request.Destination.Channel != ChannelWebhook ||
		request.Destination.DestinationRef != request.Message.DestinationRef {
		return domainErrInvalidDeliveryRequest()
	}
	endpoint, err := validateWebhookURL(request.Destination.ProviderTarget)
	if err != nil {
		return domainErrInvalidDeliveryRequest()
	}
	// A struct containing only a string cannot fail JSON marshaling.
	payload, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: formatDeliveryTurns(request.Message.Turns)})
	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	// endpoint has just passed validateWebhookURL, so request construction is
	// guaranteed for the fixed method and parsed URL.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook transport failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%w: webhook returned status %d: %s", ErrProviderRejected, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *WebhookProvider) SupportsProviderIdempotency() bool { return false }

func validateWebhookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", fmt.Errorf("%w: webhook URL is invalid", domain.ErrInvalidArgument)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: webhook URL is invalid", domain.ErrInvalidArgument)
	}
	return parsed.String(), nil
}

var _ Provider = (*WebhookProvider)(nil)
var _ IdempotentProvider = (*WebhookProvider)(nil)
