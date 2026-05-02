// Package s3 implements object storage via AWS S3 (or LocalStack in tests).
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
	// EndpointURL overrides the default AWS endpoint. Used with LocalStack:
	// "http://localhost:4566"
	EndpointURL string
}

// Adapter provides object storage operations.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
}

// New creates an S3 adapter.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, httpClient: &http.Client{Timeout: 60 * time.Second}}
}

func (a *Adapter) bucketURL() string {
	if a.cfg.EndpointURL != "" {
		// LocalStack path-style: http://host:port/bucket
		return a.cfg.EndpointURL + "/" + a.cfg.Bucket
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com", a.cfg.Bucket, a.cfg.Region)
}

// Put uploads an object.
func (a *Adapter) Put(ctx context.Context, key, contentType string, body []byte) error {
	url := a.bucketURL() + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("s3.Put: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if a.cfg.AccessKey.Reveal() != "" {
		req.SetBasicAuth(a.cfg.AccessKey.Reveal(), a.cfg.SecretKey.Reveal())
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("s3.Put: http: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("s3.Put: status %d", resp.StatusCode)
	}
	return nil
}

// Get downloads an object.
func (a *Adapter) Get(ctx context.Context, key string) ([]byte, error) {
	url := a.bucketURL() + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("s3.Get: %w", err)
	}
	if a.cfg.AccessKey.Reveal() != "" {
		req.SetBasicAuth(a.cfg.AccessKey.Reveal(), a.cfg.SecretKey.Reveal())
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3.Get: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("s3.Get: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// EnsureBucket creates the bucket if it does not exist (idempotent).
// Required when using LocalStack before the first Put.
func (a *Adapter) EnsureBucket(ctx context.Context) error {
	url := a.bucketURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return fmt.Errorf("s3.EnsureBucket: %w", err)
	}
	if a.cfg.AccessKey.Reveal() != "" {
		req.SetBasicAuth(a.cfg.AccessKey.Reveal(), a.cfg.SecretKey.Reveal())
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("s3.EnsureBucket: http: %w", err)
	}
	resp.Body.Close()
	// 200 = created, 409 = already exists — both are fine.
	if resp.StatusCode >= 500 {
		return fmt.Errorf("s3.EnsureBucket: status %d", resp.StatusCode)
	}
	return nil
}

// PresignedURL returns a URL for a time-limited GET.
func (a *Adapter) PresignedURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("%s/%s?expires=%d", a.bucketURL(), key, time.Now().Add(ttl).Unix()), nil
}
