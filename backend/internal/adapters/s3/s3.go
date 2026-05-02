// Package s3 implements object storage via AWS S3.
package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

// Config holds S3 configuration.
type Config struct {
	Region    string
	Bucket    string
	AccessKey secrets.Secret
	SecretKey secrets.Secret
}

// Adapter provides object storage operations.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a S3 adapter.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, httpClient: &http.Client{Timeout: 60 * time.Second}}
}

// Put uploads an object. key is the S3 object key; body is the content.
func (a *Adapter) Put(ctx context.Context, key, contentType string, body []byte) error {
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", a.cfg.Bucket, a.cfg.Region, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("s3.Put: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	// Real impl would sign with AWS SigV4.
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("s3.Put: http: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("s3.Put: status %d", resp.StatusCode)
	}
	return nil
}

// Get downloads an object by key.
func (a *Adapter) Get(ctx context.Context, key string) ([]byte, error) {
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", a.cfg.Bucket, a.cfg.Region, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("s3.Get: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3.Get: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("s3.Get: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// PresignedURL returns a pre-signed URL for a GET request.
func (a *Adapter) PresignedURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	// Real impl would use AWS SDK presigning. Stub returns a placeholder.
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s?expires=%d",
		a.cfg.Bucket, a.cfg.Region, key, time.Now().Add(ttl).Unix()), nil
}
