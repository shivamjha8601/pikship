// Package msg91 implements the SMS adapter using MSG91.
// Per LLD §04-adapters/msg91.
package msg91

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

const baseURL = "https://api.msg91.com/api/v5"

// Adapter sends SMS via MSG91.
type Adapter struct {
	authKey    secrets.Secret
	senderID   string
	httpClient *http.Client
}

// New creates a MSG91 adapter.
func New(authKey secrets.Secret, senderID string) *Adapter {
	return &Adapter{
		authKey:    authKey,
		senderID:   senderID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SendSMS sends a transactional SMS.
func (a *Adapter) SendSMS(ctx context.Context, to, message, templateID string) error {
	payload := map[string]any{
		"template_id": templateID,
		"short_url":   "0",
		"realTimeResponse": "1",
		"recipients": []map[string]string{
			{"mobiles": to, "var1": message},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/flow/", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("msg91.SendSMS: %w", err)
	}
	req.Header.Set("authkey", a.authKey.Reveal())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("msg91.SendSMS: http: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("msg91.SendSMS: status %d", resp.StatusCode)
	}
	return nil
}
