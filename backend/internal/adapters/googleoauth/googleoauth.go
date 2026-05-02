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

	"google.golang.org/api/idtoken"

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

// ClientID returns the configured client ID (used by the callback handler
// when validating ID tokens via google.golang.org/api/idtoken).
func (a *Adapter) ClientID() string { return a.cfg.ClientID }

// AuthURL returns the Google authorization URL.
func (a *Adapter) AuthURL(state, nonce string, forOperator bool) string {
	params := url.Values{
		"client_id":     {a.cfg.ClientID},
		"redirect_uri":  {a.cfg.RedirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"nonce":         {nonce},
		"prompt":        {"select_account"},
	}
	if forOperator {
		params.Set("hd", "pikshipp.com")
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

// TokenResponse is the subset of Google's token endpoint response we use.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// ExchangeCode swaps an authorization code for an ID token.
func (a *Adapter) ExchangeCode(ctx context.Context, code string) (TokenResponse, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {a.cfg.ClientID},
		"client_secret": {a.cfg.ClientSecret.Reveal()},
		"redirect_uri":  {a.cfg.RedirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return TokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode: status %d: %s", resp.StatusCode, string(body))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return TokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode decode: %w", err)
	}
	if tok.IDToken == "" {
		return TokenResponse{}, fmt.Errorf("googleoauth.ExchangeCode: missing id_token in response")
	}
	return tok, nil
}

// VerifyIDToken validates an ID token against Google's JWKS, checks the
// audience matches our configured ClientID, and returns the normalized
// profile plus the nonce claim. Callers MUST compare nonce against the
// per-request nonce they stored in the start cookie.
func (a *Adapter) VerifyIDToken(ctx context.Context, rawIDToken string) (identity.OAuthProfile, string, error) {
	payload, err := idtoken.Validate(ctx, rawIDToken, a.cfg.ClientID)
	if err != nil {
		return identity.OAuthProfile{}, "", fmt.Errorf("googleoauth.VerifyIDToken: %w", err)
	}
	// idtoken.Validate already checks: signature against Google's JWKS,
	// expiry, issuer (accounts.google.com / https://accounts.google.com),
	// and audience == ClientID.

	prof := identity.OAuthProfile{
		ProviderUserID: payload.Subject,
		Email:          stringClaim(payload.Claims, "email"),
		Name:           stringClaim(payload.Claims, "name"),
		PictureURL:     stringClaim(payload.Claims, "picture"),
		Raw:            payload.Claims,
	}
	if prof.Email == "" {
		return identity.OAuthProfile{}, "", fmt.Errorf("googleoauth.VerifyIDToken: id_token missing email claim")
	}
	if v, _ := payload.Claims["email_verified"].(bool); !v {
		return identity.OAuthProfile{}, "", fmt.Errorf("googleoauth.VerifyIDToken: email not verified by Google")
	}
	return prof, stringClaim(payload.Claims, "nonce"), nil
}

func stringClaim(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
