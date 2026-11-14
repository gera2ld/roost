// Package notify sends new-mail alerts to a Telegram chat through a bot.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Button is an inline URL button under a message.
type Button struct {
	Text string
	URL  string
}

// Sender posts one HTML-formatted message.
type Sender interface {
	Send(ctx context.Context, html string, button *Button) error
}

// Telegram calls the Bot API's sendMessage.
type Telegram struct {
	Token   string
	ChatID  string
	BaseURL string // default https://api.telegram.org
	HTTP    *http.Client
}

// APIError is a Bot API failure. RetryAfter is set on rate limiting (429).
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

func (t *Telegram) Send(ctx context.Context, html string, button *Button) error {
	payload := map[string]any{
		"chat_id":              t.ChatID,
		"text":                 html,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	}
	if button != nil {
		payload["reply_markup"] = map[string]any{
			"inline_keyboard": [][]map[string]string{{{"text": button.Text, "url": button.URL}}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+t.Token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		// url.Error embeds the request URL, which contains the bot token.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("telegram: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var out struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("telegram: HTTP %d", res.StatusCode)
	}
	if !out.OK {
		code := out.ErrorCode
		if code == 0 {
			code = res.StatusCode
		}
		return &APIError{Code: code, Description: out.Description,
			RetryAfter: time.Duration(out.Parameters.RetryAfter) * time.Second}
	}
	return nil
}
