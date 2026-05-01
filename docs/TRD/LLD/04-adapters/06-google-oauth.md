# Google OAuth Adapter

## Purpose

Sellers and operators authenticate via Google OAuth (Sign-in with Google). This adapter wraps Google's OAuth 2.0 + OIDC flow and emits a `(google_subject, email, name)` triple that the identity service uses to upsert an `app_user` and create a session.

## Package Layout

```
internal/vendors/oauth/google/
├── adapter.go             // OAuth flow handlers
├── verifier.go            // ID-token verification (JWKS)
├── repo.go                // Nonce store
├── adapter_test.go
└── verifier_test.go
```

## Configuration

```go
type Config struct {
    ClientID     string
    ClientSecret secrets.Secret[string]
    RedirectURI  string                     // https://api.pikshipp.com/auth/google/callback
    AllowedHostedDomains []string           // optional: ["pikshipp.com"] for ops accounts
    HTTP         *framework.HTTPClient
}
```

## OAuth Flow

```
1. GET /auth/google/start?intent=seller|operator
     - Generate `state` nonce + `nonce` (for OIDC)
     - Persist (state → intent) for 10 min
     - Redirect to Google's authorization endpoint

2. GET /auth/google/callback?code=...&state=...
     - Verify state nonce; load intent
     - Exchange code → tokens
     - Verify id_token signature against Google's JWKS
     - Verify nonce inside id_token matches
     - Verify hd claim if AllowedHostedDomains is non-empty (operator path)
     - Upsert user via identity.UpsertFromOAuth
     - Create session; set cookie
     - Redirect to /onboarding (seller) or /admin (operator)
```

## Implementation

### Start Handler

```go
package google

func StartHandler(svc *Service) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        intent := r.URL.Query().Get("intent")
        if intent != "seller" && intent != "operator" {
            intent = "seller"
        }
        state := generateNonce()
        oidcNonce := generateNonce()
        if err := svc.PersistNonce(r.Context(), state, oidcNonce, intent); err != nil {
            http.Error(w, "internal", http.StatusInternalServerError)
            return
        }
        params := url.Values{}
        params.Set("client_id", svc.cfg.ClientID)
        params.Set("redirect_uri", svc.cfg.RedirectURI)
        params.Set("response_type", "code")
        params.Set("scope", "openid email profile")
        params.Set("state", state)
        params.Set("nonce", oidcNonce)
        params.Set("prompt", "select_account")
        if intent == "operator" && len(svc.cfg.AllowedHostedDomains) > 0 {
            params.Set("hd", svc.cfg.AllowedHostedDomains[0])
        }
        http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+params.Encode(), http.StatusFound)
    })
}
```

### Callback Handler

```go
func CallbackHandler(svc *Service, identity identity.Service, auth auth.Authenticator) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        code := r.URL.Query().Get("code")
        state := r.URL.Query().Get("state")
        if code == "" || state == "" {
            http.Error(w, "missing params", http.StatusBadRequest)
            return
        }
        nonceData, err := svc.ConsumeNonce(r.Context(), state)
        if err != nil {
            http.Error(w, "bad state", http.StatusUnauthorized)
            return
        }
        tok, err := svc.ExchangeCode(r.Context(), code)
        if err != nil {
            slog.Warn("google oauth: code exchange", "err", err)
            http.Error(w, "exchange failed", http.StatusBadGateway)
            return
        }
        claims, err := svc.VerifyIDToken(r.Context(), tok.IDToken, nonceData.OIDCNonce)
        if err != nil {
            slog.Warn("google oauth: id token verify", "err", err)
            http.Error(w, "bad id token", http.StatusUnauthorized)
            return
        }
        // Verify hosted domain for operator intent
        if nonceData.Intent == "operator" {
            if !slices.Contains(svc.cfg.AllowedHostedDomains, claims.HD) {
                http.Error(w, "operator account requires Pikshipp domain", http.StatusForbidden)
                return
            }
        }

        user, err := identity.UpsertFromOAuth(r.Context(), identity.UpsertOAuthRequest{
            Provider:        "google",
            ProviderSubject: claims.Sub,
            Email:           claims.Email,
            EmailVerified:   claims.EmailVerified,
            Name:            claims.Name,
            Picture:         claims.Picture,
        })
        if err != nil {
            slog.Error("google oauth: upsert user", "err", err)
            http.Error(w, "internal", http.StatusInternalServerError)
            return
        }

        sessionTok, err := auth.IssueSession(r.Context(), auth.IssueRequest{
            UserID: user.ID,
            UserAgent: r.UserAgent(),
            IP: clientIP(r),
            Reason: "google_oauth",
        })
        if err != nil {
            http.Error(w, "session", http.StatusInternalServerError)
            return
        }
        setSessionCookie(w, sessionTok, svc.cfg.SessionCookieDomain)

        redirect := "/onboarding"
        if nonceData.Intent == "operator" {
            redirect = "/admin"
        }
        http.Redirect(w, r, redirect, http.StatusFound)
    })
}
```

### ExchangeCode

```go
func (s *Service) ExchangeCode(ctx context.Context, code string) (*tokenResponse, error) {
    body := url.Values{}
    body.Set("code", code)
    body.Set("client_id", s.cfg.ClientID)
    body.Set("client_secret", s.cfg.ClientSecret.Reveal())
    body.Set("redirect_uri", s.cfg.RedirectURI)
    body.Set("grant_type", "authorization_code")

    var resp tokenResponse
    httpResp, err := s.cfg.HTTP.PostForm(ctx, "https://oauth2.googleapis.com/token", body, &resp)
    if err != nil {
        return nil, err
    }
    _ = httpResp
    return &resp, nil
}

type tokenResponse struct {
    AccessToken  string `json:"access_token"`
    IDToken      string `json:"id_token"`
    RefreshToken string `json:"refresh_token,omitempty"`
    ExpiresIn    int    `json:"expires_in"`
    TokenType    string `json:"token_type"`
}
```

### VerifyIDToken (JWKS)

```go
type Claims struct {
    Sub           string  `json:"sub"`
    Iss           string  `json:"iss"`
    Aud           string  `json:"aud"`
    Email         string  `json:"email"`
    EmailVerified bool    `json:"email_verified"`
    Name          string  `json:"name"`
    Picture       string  `json:"picture"`
    HD            string  `json:"hd"`
    Nonce         string  `json:"nonce"`
    Exp           int64   `json:"exp"`
    Iat           int64   `json:"iat"`
}

type jwksCache struct {
    mu        sync.RWMutex
    keys      map[string]*rsa.PublicKey
    fetchedAt time.Time
    ttl       time.Duration
}

func (s *Service) VerifyIDToken(ctx context.Context, idToken, expectedNonce string) (*Claims, error) {
    parts := strings.Split(idToken, ".")
    if len(parts) != 3 {
        return nil, errors.New("malformed jwt")
    }
    headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
    if err != nil {
        return nil, err
    }
    var header struct {
        Alg string `json:"alg"`
        Kid string `json:"kid"`
    }
    if err := json.Unmarshal(headerJSON, &header); err != nil {
        return nil, err
    }
    if header.Alg != "RS256" {
        return nil, fmt.Errorf("unsupported alg %q", header.Alg)
    }
    pub, err := s.jwks.Get(ctx, header.Kid)
    if err != nil {
        return nil, err
    }
    signingInput := parts[0] + "." + parts[1]
    sig, err := base64.RawURLEncoding.DecodeString(parts[2])
    if err != nil {
        return nil, err
    }
    h := sha256.Sum256([]byte(signingInput))
    if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig); err != nil {
        return nil, errors.New("bad signature")
    }
    claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
    if err != nil {
        return nil, err
    }
    var c Claims
    if err := json.Unmarshal(claimsJSON, &c); err != nil {
        return nil, err
    }
    if c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com" {
        return nil, errors.New("bad issuer")
    }
    if c.Aud != s.cfg.ClientID {
        return nil, errors.New("bad aud")
    }
    now := time.Now().Unix()
    if now > c.Exp {
        return nil, errors.New("expired")
    }
    if now < c.Iat-60 {
        return nil, errors.New("issued in future")
    }
    if c.Nonce != expectedNonce {
        return nil, errors.New("nonce mismatch")
    }
    if !c.EmailVerified {
        return nil, errors.New("email not verified")
    }
    return &c, nil
}

func (j *jwksCache) Get(ctx context.Context, kid string) (*rsa.PublicKey, error) {
    j.mu.RLock()
    if time.Since(j.fetchedAt) < j.ttl {
        if k, ok := j.keys[kid]; ok {
            j.mu.RUnlock()
            return k, nil
        }
    }
    j.mu.RUnlock()
    if err := j.refresh(ctx); err != nil {
        return nil, err
    }
    j.mu.RLock()
    defer j.mu.RUnlock()
    if k, ok := j.keys[kid]; ok {
        return k, nil
    }
    return nil, fmt.Errorf("kid %q not in JWKS", kid)
}

func (j *jwksCache) refresh(ctx context.Context) error {
    // Fetch https://www.googleapis.com/oauth2/v3/certs (JWKS)
    // Parse RSA keys; populate j.keys + fetchedAt.
}
```

The JWKS cache TTL is 1 hour (Google rotates keys infrequently).

## Nonce Storage

```sql
CREATE TABLE oauth_nonce (
    state         text       PRIMARY KEY,
    oidc_nonce    text       NOT NULL,
    intent        text       NOT NULL,        -- "seller" | "operator"
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL
);

CREATE INDEX oauth_nonce_expires_idx ON oauth_nonce(expires_at);

GRANT SELECT, INSERT, DELETE ON oauth_nonce TO pikshipp_app;
```

GC sweep deletes expired rows hourly.

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `StartHandler` | 5 ms | 18 ms | INSERT nonce + redirect |
| `CallbackHandler` end-to-end | 600 ms | 1.5 s | Google round-trips dominate |
| `VerifyIDToken` (JWKS hit) | 1.5 ms | 5 ms | RSA verify |
| `VerifyIDToken` (JWKS miss) | 250 ms | 800 ms | + JWKS fetch |

## Failure Modes

| Failure | Class |
|---|---|
| Bad state nonce | reject 401; log; possible CSRF |
| ID token signature invalid | 401; log loudly (clock skew or compromised JWKS source) |
| nonce in id_token doesn't match stored | 401; replay attempt suspected |
| `email_verified=false` | 403; ask user to verify in Google account |
| Operator login from non-Pikshipp domain | 403 |
| JWKS fetch fails | 503 to user; jwks cache retains stale keys for grace period (5 min) |
| Token exchange returns 5xx | 502 to user; retry safe |

## Testing

```go
func TestVerifyIDToken_GoldenJWT(t *testing.T) {
    // Use a fixture JWKS + a JWT signed with that JWKS; assert claims.
}
func TestVerifyIDToken_BadSignature(t *testing.T) { /* tampered JWT → reject */ }
func TestVerifyIDToken_ExpiredToken(t *testing.T) { /* exp in past → reject */ }
func TestVerifyIDToken_NonceMismatch(t *testing.T) { /* ... */ }
func TestVerifyIDToken_BadAudience(t *testing.T) { /* ... */ }
func TestStartHandler_BuildsCorrectURL(t *testing.T) { /* ... */ }
func TestCallbackHandler_E2E_MockGoogle_SLT(t *testing.T) {
    // httptest.Server simulates Google /token + JWKS; full callback path.
}
func TestNonceConsumeOnce_SLT(t *testing.T) { /* second use rejected */ }
func TestOperatorLogin_BlockedFromExternalDomain_SLT(t *testing.T) { /* hd check */ }
```

## Open Questions

1. **Refresh tokens.** We don't currently store Google refresh tokens (we don't call Google APIs after sign-in). **Decision:** keep as-is.
2. **Multi-provider OAuth.** Add Microsoft / Apple later. Adapter interface generalized into `OAuthProvider`. **Decision:** v1+.
3. **PKCE.** Currently we use the OAuth confidential client flow with secret. PKCE adds defense in depth. **Decision:** add at v0.5; low risk current state.
4. **Session-cookie domain.** `*.pikshipp.com` for cross-subdomain. **Decision:** documented in config.

## References

- LLD §03-services/08-identity: `UpsertFromOAuth`.
- LLD §02-infrastructure/06-auth: `auth.Authenticator.IssueSession`.
- LLD §02-infrastructure/05-secrets: client secret storage.
