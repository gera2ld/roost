// Package outbound sends mail through Resend and stores what was sent the same
// way inbound mail is stored: a write-once raw EML plus indexed rows, threaded
// into the conversation it answers.
package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Attachment is a file sent with an email.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Email is one message handed to the provider. Addresses are RFC 5322 strings.
type Email struct {
	From        string
	To, Cc, Bcc []string
	ReplyTo     []string
	Subject     string
	Text        string
	Headers     map[string]string
	Attachments []Attachment
}

// Sender delivers an email and returns the provider's id for it. The idempotency
// key makes a retried call return the first result instead of sending twice.
type Sender interface {
	Send(ctx context.Context, email Email, idempotencyKey string) (string, error)
}

// Resend calls the Resend HTTP API.
type Resend struct {
	APIKey  string
	BaseURL string // default https://api.resend.com
	HTTP    *http.Client
}

// APIError is a non-2xx answer from Resend.
type APIError struct {
	Status  int
	Name    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("resend: %d %s: %s", e.Status, e.Name, e.Message)
}

type resendAttachment struct {
	Filename    string `json:"filename"`
	Content     []byte `json:"content"` // base64 via encoding/json
	ContentType string `json:"content_type,omitempty"`
}

type resendRequest struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Cc          []string           `json:"cc,omitempty"`
	Bcc         []string           `json:"bcc,omitempty"`
	ReplyTo     []string           `json:"reply_to,omitempty"`
	Subject     string             `json:"subject"`
	Text        string             `json:"text"`
	Headers     map[string]string  `json:"headers,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

func (r *Resend) Send(ctx context.Context, email Email, idempotencyKey string) (string, error) {
	body := resendRequest{
		From: email.From, To: nonNil(email.To), Cc: email.Cc, Bcc: email.Bcc, ReplyTo: email.ReplyTo,
		Subject: email.Subject, Text: email.Text, Headers: email.Headers,
	}
	for _, a := range email.Attachments {
		body.Attachments = append(body.Attachments, resendAttachment{a.Filename, a.Content, a.ContentType})
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	base := r.BaseURL
	if base == "" {
		base = "https://api.resend.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emails", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		apiErr := &APIError{Status: res.StatusCode}
		var named struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &named) == nil {
			apiErr.Name, apiErr.Message = named.Name, named.Message
		}
		if apiErr.Message == "" {
			apiErr.Message = string(bytes.TrimSpace(raw))
		}
		return "", apiErr
	}
	var ok struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &ok); err != nil || ok.ID == "" {
		return "", fmt.Errorf("resend: unexpected response %q", raw)
	}
	return ok.ID, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
