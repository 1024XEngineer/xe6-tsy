// Package languageconfig adapts the API-owned language configuration endpoint to command execution.
package languageconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
)

const (
	systemTokenHeader  = "X-Lingow-System-Token"
	defaultTimeout     = 3 * time.Second
	maxResponseBytes   = int64(64 << 10)
	minSystemTokenSize = 32
)

var (
	ErrConfigurationInvalid = errors.New("command language-config client configuration is invalid")
	ErrResponseInvalid      = errors.New("command language-config response is invalid")
	ErrResponseTooLarge     = errors.New("command language-config response is too large")
)

// HTTPDoer is the narrow transport dependency used by Client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Config contains only internal API transport settings. SystemToken is never logged or returned.
type Config struct {
	BaseURL     string
	SystemToken string
	HTTP        HTTPDoer
	Timeout     time.Duration
}

// Client calls the API control plane and never reads or writes its database directly.
type Client struct {
	baseURL     *url.URL
	systemToken string
	http        HTTPDoer
	timeout     time.Duration
}

// HTTPError preserves the stable API error code without coupling realtime to API implementation types.
type HTTPError struct {
	StatusCode int
	Code       string
}

func (e *HTTPError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("command language-config API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("command language-config API returned HTTP %d (%s)", e.StatusCode, e.Code)
}

// NewClient validates the internal endpoint before any command can execute.
func NewClient(config Config) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("%w: base URL", ErrConfigurationInvalid)
	}
	token := strings.TrimSpace(config.SystemToken)
	if len([]byte(token)) < minSystemTokenSize {
		return nil, fmt.Errorf("%w: system token must contain at least %d bytes", ErrConfigurationInvalid, minSystemTokenSize)
	}
	if config.HTTP == nil {
		config.HTTP = http.DefaultClient
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	return &Client{baseURL: baseURL, systemToken: token, http: config.HTTP, timeout: config.Timeout}, nil
}

// Configure creates or replays one language snapshot. A successful response must echo the
// request identity, preventing a proxy or routing error from switching mode against stale data.
func (c *Client) Configure(ctx context.Context, request languagesv1.CommandConfigRequest) (languagesv1.CommandConfigResult, error) {
	if err := ctx.Err(); err != nil {
		return languagesv1.CommandConfigResult{}, err
	}
	if c == nil || c.baseURL == nil || c.http == nil || request.Validate() != nil {
		return languagesv1.CommandConfigResult{}, ErrConfigurationInvalid
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return languagesv1.CommandConfigResult{}, fmt.Errorf("encode command language configuration: %w", err)
	}
	endpoint, err := url.JoinPath(c.baseURL.String(), "internal", "v1", "voice-sessions", request.SessionID, "language-config")
	if err != nil {
		return languagesv1.CommandConfigResult{}, fmt.Errorf("build command language-config URL: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return languagesv1.CommandConfigResult{}, fmt.Errorf("create command language-config request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(systemTokenHeader, c.systemToken)
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return languagesv1.CommandConfigResult{}, fmt.Errorf("call command language-config API: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body)
	if err != nil {
		return languagesv1.CommandConfigResult{}, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return languagesv1.CommandConfigResult{}, decodeHTTPError(response.StatusCode, body)
	}
	var result languagesv1.CommandConfigResult
	if err := decodeStrict(body, &result); err != nil || result.SessionID != request.SessionID ||
		result.CommandID != request.CommandID || result.Version <= 0 {
		return languagesv1.CommandConfigResult{}, ErrResponseInvalid
	}
	return result, nil
}

func readBounded(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read command language-config response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func decodeHTTPError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = decodeStrict(body, &envelope)
	return &HTTPError{StatusCode: status, Code: strings.TrimSpace(envelope.Error.Code)}
}

var _ command.LanguageConfigurator = (*Client)(nil)
