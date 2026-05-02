// Package ses implements email sending via AWS SES.
// Per LLD §04-adapters/ses.
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
	APIKey   secrets.Secret // AWS access key ID (simplified; real impl uses AWS SDK)
}

// Message is one email to send.
type Message struct {
	To      []string
	Subject string
	HTML    string
	Text    string
}

// Adapter sends emails via SES HTTP API.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a SES adapter.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, httpClient: &http.Client{Timeout: 15 * time.Second}}
}

// Send delivers one email via SES.
func (a *Adapter) Send(ctx context.Context, msg Message) error {
	// In production this would use the AWS SDK (aws-sdk-go-v2/service/sesv2).
	// This is a stub that logs the intent.
	endpoint := fmt.Sprintf("https://email.%s.amazonaws.com/v2/email/outbound-emails", a.cfg.Region)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ses.Send: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Real impl would sign the request with AWS SigV4.
	_ = a.cfg.APIKey

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ses.Send: http: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ses.Send: status %d", resp.StatusCode)
	}
	return nil
}
