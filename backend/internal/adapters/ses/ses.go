// Package ses implements email sending via AWS SES (or LocalStack in tests).
package ses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

// Config holds SES configuration.
type Config struct {
	Region   string
	FromAddr string
	APIKey   secrets.Secret
	// EndpointURL overrides the AWS endpoint. Used with LocalStack:
	// "http://localhost:4566"
	EndpointURL string
}

// Message is one email to send.
type Message struct {
	To      []string
	Subject string
	HTML    string
	Text    string
}

// Adapter sends emails.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
}

// New creates an SES adapter.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, httpClient: &http.Client{Timeout: 15 * time.Second}}
}

func (a *Adapter) endpoint() string {
	if a.cfg.EndpointURL != "" {
		return a.cfg.EndpointURL
	}
	return fmt.Sprintf("https://email.%s.amazonaws.com", a.cfg.Region)
}

// Send delivers one email.
func (a *Adapter) Send(ctx context.Context, msg Message) error {
	url := a.endpoint() + "/v2/email/outbound-emails"
	payload := map[string]any{
		"FromEmailAddress": a.cfg.FromAddr,
		"Destination":      map[string]any{"ToAddresses": msg.To},
		"Content": map[string]any{
			"Simple": map[string]any{
				"Subject": map[string]any{"Data": msg.Subject},
				"Body": map[string]any{
					"Html": map[string]any{"Data": msg.HTML},
					"Text": map[string]any{"Data": msg.Text},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ses.Send: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey.Reveal() != "" {
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+a.cfg.APIKey.Reveal())
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ses.Send: http: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ses.Send: status %d", resp.StatusCode)
	}
	return nil
}

// VerifyEmailIdentity registers an email address with SES (required in
// sandbox mode and LocalStack before sending). Idempotent.
func (a *Adapter) VerifyEmailIdentity(ctx context.Context, email string) error {
	url := a.endpoint() + "/v2/email/identities"
	payload := map[string]any{"EmailAddress": email}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ses.VerifyEmailIdentity: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ses.VerifyEmailIdentity: http: %w", err)
	}
	resp.Body.Close()
	return nil
}
