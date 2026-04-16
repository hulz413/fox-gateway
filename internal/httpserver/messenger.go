package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"fox-gateway/internal/approval"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type LarkClient struct {
	appID      string
	appSecret  string
	httpClient *http.Client
	client     *lark.Client
	logger     *log.Logger

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func truncateForCard(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func NewLarkClient(appID, appSecret string, logger *log.Logger) *LarkClient {
	return &LarkClient{
		appID:      appID,
		appSecret:  appSecret,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		client:     lark.NewClient(appID, appSecret),
		logger:     logger,
	}
}

func (c *LarkClient) SendText(ctx context.Context, chatID, text string) error {
	if c.logger != nil {
		c.logger.Printf("sending Feishu text reply: chat=%s text=%q", chatID, text)
	}
	return c.sendMessage(ctx, chatID, "text", map[string]string{"text": text})
}

func (c *LarkClient) ValidateCredentials(ctx context.Context) error {
	_, err := c.tenantAccessToken(ctx)
	return err
}

func (c *LarkClient) SendOneSecondAck(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("Lark client is not initialized")
	}
	_, err := c.client.Im.V1.MessageReaction.Create(ctx,
		larkim.NewCreateMessageReactionReqBuilder().
			MessageId(messageID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(larkim.NewEmojiBuilder().EmojiType("OneSecond").Build()).
				Build()).
			Build(),
	)
	if err == nil {
		return nil
	}
	return nil
}

func (c *LarkClient) SendDecisionCard(ctx context.Context, chatID string, card approval.DecisionCard) error {
	if c.logger != nil {
		c.logger.Printf("sending Feishu decision card: chat=%s job=%s kind=%s", chatID, card.JobID, card.RequestKind)
	}
	content, err := buildDecisionCardPayload(card)
	if err != nil {
		return err
	}
	return c.sendMessage(ctx, chatID, "interactive", content)
}

func buildDecisionCardPayload(card approval.DecisionCard) (map[string]any, error) {
	theme := strings.TrimSpace(card.Theme)
	if theme == "" {
		theme = "orange"
	}
	actions := make([]any, 0, len(card.Choices))
	for _, choice := range card.Choices {
		buttonType := "default"
		if strings.TrimSpace(choice.Style) != "" {
			buttonType = choice.Style
		}
		actions = append(actions, map[string]any{
			"tag":   "button",
			"type":  buttonType,
			"text":  map[string]any{"tag": "plain_text", "content": choice.Label},
			"value": map[string]string{"job_id": card.JobID, "request_kind": card.RequestKind, "choice_id": choice.ID},
		})
	}
	content := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": theme,
			"title": map[string]any{
				"tag":     "plain_text",
				"content": truncateForCard(card.Title, 120),
			},
		},
		"elements": []any{
			map[string]any{
				"tag":     "markdown",
				"content": truncateForCard(card.Body, 800),
			},
			map[string]any{
				"tag":     "action",
				"actions": actions,
			},
		},
	}
	return content, nil
}

func (c *LarkClient) SendApprovalCard(ctx context.Context, chatID, jobID, hash, summary string) error {
	return c.SendDecisionCard(ctx, chatID, approval.DecisionCard{
		JobID:       jobID,
		RequestKind: approval.KindApproval,
		Title:       "Approval required",
		Body:        fmt.Sprintf("**Job**: `%s`\n**Hash**: `%s`\n**Request**: %s", jobID, truncateForCard(hash, 24), truncateForCard(summary, 500)),
		Theme:       "orange",
		Choices: []approval.Choice{
			{ID: "approve", Label: "Approve", Style: "primary"},
			{ID: "reject", Label: "Reject", Style: "danger"},
		},
	})
}

func (c *LarkClient) sendMessage(ctx context.Context, chatID, msgType string, content any) error {
	if strings.TrimSpace(c.appID) == "" || strings.TrimSpace(c.appSecret) == "" {
		return fmt.Errorf("LARK_APP_ID or LARK_APP_SECRET is not configured")
	}
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	contentBody, err := json.Marshal(content)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"receive_id": chatID,
		"msg_type":   msgType,
		"content":    string(contentBody),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.larksuite.com/open-apis/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("lark send message failed: %s", strings.TrimSpace(string(data)))
	}
	return nil
}

func (c *LarkClient) tenantAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		defer c.mu.Unlock()
		return c.token, nil
	}
	c.mu.Unlock()

	payload := map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.larksuite.com/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("lark token fetch failed: %s", strings.TrimSpace(string(data)))
	}
	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("lark token fetch failed: %s", result.Msg)
	}
	c.mu.Lock()
	c.token = result.TenantAccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(result.Expire-60) * time.Second)
	c.mu.Unlock()
	return result.TenantAccessToken, nil
}
