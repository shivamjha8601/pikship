// Package googleoauth implements Google OIDC OAuth flow.
// Per LLD §04-adapters/06-google-oauth.
package googleoauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/identity"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

// Config holds Google OAuth configuration.
type Config struct {
	ClientID     string
	ClientSecret secrets.Secret
	RedirectURI  string
}

// Adapter implements Google OAuth 2.0 OIDC flow.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a Google OAuth adapter.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// AuthURL returns the Google authorization URL.
func (a *Adapter) AuthURL(state, nonce string, forOperator bool) string {
	params := url.Values{
		"client_id":    {a.cfg.ClientID},
		"redirect_uri": {a.cfg.RedirectURI},
		"response_type": {"code"},
		"scope":        {"openid email profile"},
		"state":        {state},
		"nonce":        {nonce},
		"prompt":       {"select_account"},
	}
	if forOperator {
		params.Set("hd", "pikshipp.com")
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// ExchangeCode exchanges an auth code for tokens.
func (a *Adapter) ExchangeCode(ctx context.Context, code string) (tokenResponse, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {a.cfg.ClientID},
		"client_secret": {a.cfg.ClientSecret.Reveal()},
		"redirect_uri":  {a.cfg.RedirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return tokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode decode: %w", err)
	}
	return tok, nil
}

// ExtractProfile parses the ID token claims (without verifying signature here —
// production code should verify via JWKS).
func (a *Adapter) ExtractProfile(idToken string) (identity.OAuthProfile, error) {
	// Split JWT and decode payload.
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return identity.OAuthProfile{}, fmt.Errorf("googleoauth: invalid id_token format")
	}
	// Base64-url decode claims (padding insensitive).
	padded := parts[1]
	for len(padded)%4 != 0 {
		padded += "="
	}
	decodedBytes := make([]byte, len(padded))
	n, err := decodeBase64URL([]byte(padded), decodedBytes)
	if err != nil {
		return identity.OAuthProfile{}, fmt.Errorf("googleoauth: decode claims: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(decodedBytes[:n], &claims); err != nil {
		return identity.OAuthProfile{}, fmt.Errorf("googleoauth: parse claims: %w", err)
	}
	return identity.OAuthProfile{
		ProviderUserID: stringClaim(claims, "sub"),
		Email:          stringClaim(claims, "email"),
		Name:           stringClaim(claims, "name"),
		PictureURL:     stringClaim(claims, "picture"),
		Raw:            claims,
	}, nil
}

func stringClaim(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func decodeBase64URL(src, dst []byte) (int, error) {
	import_base64 := func(c byte) (byte, bool) {
		switch {
		case c >= 'A' && c <= 'Z':
			return c - 'A', true
		case c >= 'a' && c <= 'z':
			return c - 'a' + 26, true
		case c >= '0' && c <= '9':
			return c - '0' + 52, true
		case c == '-':
			return 62, true
		case c == '_':
			return 63, true
		}
		return 0, false
	}
	// Simple base64url decode (handles no-padding variant).
	n := 0
	for i := 0; i+3 < len(src); i += 4 {
		a, _ := import_base64(src[i])
		b, _ := import_base64(src[i+1])
		c, _ := import_base64(src[i+2])
		d, _ := import_base64(src[i+3])
		if n+2 < len(dst) {
			dst[n] = a<<2 | b>>4
			dst[n+1] = b<<4 | c>>2
			dst[n+2] = c<<6 | d
			n += 3
		}
	}
	return n, nil
}
