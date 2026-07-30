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
	"sync"
	"time"
)

const weComAPIBase = "https://qyapi.weixin.qq.com"

// WeComClient exchanges OAuth codes and sends WeChat Work application messages.
type WeComClient struct {
	corpID     string
	corpSecret string
	agentID    int
	apiBase    string
	httpClient *http.Client
	tokenMu    sync.Mutex
	token      string
	tokenUntil time.Time
}

// WeComConfig configures a shared WeCom API client for bind and delivery.
type WeComConfig struct {
	CorpID     string
	CorpSecret string
	AgentID    int
	APIBase    string
}

func NewWeComClient(config WeComConfig) (*WeComClient, error) {
	config.CorpID = strings.TrimSpace(config.CorpID)
	config.CorpSecret = strings.TrimSpace(config.CorpSecret)
	config.APIBase = strings.TrimRight(strings.TrimSpace(config.APIBase), "/")
	if config.APIBase == "" {
		config.APIBase = weComAPIBase
	}
	if config.CorpID == "" || config.CorpSecret == "" || config.AgentID <= 0 {
		return nil, fmt.Errorf("wecom corp id, corp secret, and agent id are required")
	}
	return &WeComClient{
		corpID:     config.CorpID,
		corpSecret: config.CorpSecret,
		agentID:    config.AgentID,
		apiBase:    config.APIBase,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *WeComClient) UserIDFromOAuthCode(ctx context.Context, code string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("wecom client is not configured")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("wecom oauth code is required")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}
	endpoint := c.apiBase + "/cgi-bin/auth/getuserinfo?access_token=" + url.QueryEscape(token) + "&code=" + url.QueryEscape(code)
	var response struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		UserID  string `json:"userid"`
	}
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return "", err
	}
	if response.ErrCode != 0 {
		return "", mapWeComOAuthError(response.ErrCode, response.ErrMsg)
	}
	userid, err := validateWeComUserID(response.UserID)
	if err != nil {
		return "", err
	}
	return userid, nil
}

func (c *WeComClient) SendTextMessage(ctx context.Context, userid, content string) error {
	if c == nil {
		return fmt.Errorf("wecom client is not configured")
	}
	userid, err := validateWeComUserID(userid)
	if err != nil {
		return err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("wecom message content is required")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"touser":  userid,
		"msgtype": "text",
		"agentid": c.agentID,
		"text":    map[string]string{"content": content},
	})
	if err != nil {
		return err
	}
	endpoint := c.apiBase + "/cgi-bin/message/send?access_token=" + url.QueryEscape(token)
	var response struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := c.postJSON(ctx, endpoint, payload, &response); err != nil {
		return err
	}
	if response.ErrCode != 0 {
		if isWeComTokenRefreshError(response.ErrCode) {
			c.invalidateToken()
		}
		return mapWeComSendError(response.ErrCode, response.ErrMsg)
	}
	return nil
}

func (c *WeComClient) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenUntil) {
		return c.token, nil
	}
	endpoint := c.apiBase + "/cgi-bin/gettoken?corpid=" + url.QueryEscape(c.corpID) + "&corpsecret=" + url.QueryEscape(c.corpSecret)
	var response struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return "", err
	}
	if response.ErrCode != 0 || response.AccessToken == "" {
		if isWeComTokenRefreshError(response.ErrCode) {
			c.token = ""
			c.tokenUntil = time.Time{}
		}
		return "", fmt.Errorf("wecom gettoken: %s (code %d)", response.ErrMsg, response.ErrCode)
	}
	c.token = response.AccessToken
	ttl := time.Duration(response.ExpiresIn) * time.Second
	if ttl <= time.Minute {
		ttl = time.Hour
	}
	c.tokenUntil = time.Now().Add(ttl - time.Minute)
	return c.token, nil
}

func (c *WeComClient) invalidateToken() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = ""
	c.tokenUntil = time.Time{}
}

func (c *WeComClient) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return sanitizeWeComTransportError(err)
	}
	defer response.Body.Close()
	return decodeWeComResponse(response.Body, target)
}

func (c *WeComClient) postJSON(ctx context.Context, endpoint string, payload []byte, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return sanitizeWeComTransportError(err)
	}
	defer response.Body.Close()
	return decodeWeComResponse(response.Body, target)
}

func decodeWeComResponse(body io.Reader, target any) error {
	if err := json.NewDecoder(body).Decode(target); err != nil {
		return fmt.Errorf("decode wecom response: %w", err)
	}
	return nil
}

var _ WeComIdentityClient = (*WeComClient)(nil)
var _ WeComMessenger = (*WeComClient)(nil)
