package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
		Transport:     webhookTransport(),
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
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !isPublicWebhookIP(ip) {
		return "", fmt.Errorf("%w: webhook URL targets a private network", domain.ErrInvalidArgument)
	}
	return parsed.String(), nil
}

func webhookTransport() http.RoundTripper {
	return &http.Transport{
		DialContext:         dialPublicWebhookContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// dialPublicWebhookContext resolves the host itself, rejects non-public
// addresses, and connects to the checked IP. This prevents a DNS answer from
// changing between validation and connection (DNS rebinding).
func dialPublicWebhookContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicWebhookIP(ip) {
			return nil, fmt.Errorf("webhook address targets a private network")
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve webhook host: %w", err)
	}
	for _, ip := range ips {
		if !isPublicWebhookIP(ip) {
			continue
		}
		conn, dialErr := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("webhook host resolved only to private or unreachable addresses")
}

func isPublicWebhookIP(ip net.IP) bool {
	return ip != nil &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() &&
		!ip.IsMulticast()
}

var _ Provider = (*WebhookProvider)(nil)
var _ IdempotentProvider = (*WebhookProvider)(nil)
