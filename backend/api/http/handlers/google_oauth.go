package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/adapters/googleoauth"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
)

// GoogleOAuthAdapter is the subset of *googleoauth.Adapter the handlers need.
// Defined as an interface to keep tests easy to stub.
type GoogleOAuthAdapter interface {
	AuthURL(state, nonce string, forOperator bool) string
	ExchangeCode(ctx context.Context, code string) (googleoauth.TokenResponse, error)
	VerifyIDToken(ctx context.Context, rawIDToken string) (identity.OAuthProfile, string, error)
}

const (
	googleStateCookie = "pikshipp_g_state"
	googleNonceCookie = "pikshipp_g_nonce"
	googleCookieTTL   = 10 * time.Minute
)

// GoogleStartHandler kicks off the OAuth flow: generate state + nonce,
// store both in short-lived httpOnly cookies, and 302 to Google.
func GoogleStartHandler(d OnboardingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Google == nil {
			http.Error(w, "google oauth not configured", http.StatusServiceUnavailable)
			return
		}
		state, err := randomToken(24)
		if err != nil {
			writeError(w, r, err)
			return
		}
		nonce, err := randomToken(24)
		if err != nil {
			writeError(w, r, err)
			return
		}
		setOAuthCookie(w, r, googleStateCookie, state)
		setOAuthCookie(w, r, googleNonceCookie, nonce)
		http.Redirect(w, r, d.Google.AuthURL(state, nonce, false), http.StatusFound)
	}
}

// GoogleCallbackHandler receives Google's redirect: validates state cookie,
// exchanges the code, verifies the ID token (signature + audience + nonce),
// upserts the user, issues an opaque session token, and 302s to the
// configured frontend URL with #token=...&expires_at=...&new_user=...
func GoogleCallbackHandler(d OnboardingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Google == nil {
			http.Error(w, "google oauth not configured", http.StatusServiceUnavailable)
			return
		}

		if e := r.URL.Query().Get("error"); e != "" {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, e)
			return
		}

		stateQ := r.URL.Query().Get("state")
		stateCookie, err := r.Cookie(googleStateCookie)
		if err != nil || stateCookie.Value == "" || stateQ == "" ||
			subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(stateQ)) != 1 {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "state_mismatch")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "missing_code")
			return
		}
		tok, err := d.Google.ExchangeCode(r.Context(), code)
		if err != nil {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "exchange_failed")
			return
		}

		profile, idTokenNonce, err := d.Google.VerifyIDToken(r.Context(), tok.IDToken)
		if err != nil {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "id_token_invalid")
			return
		}
		nonceCookie, err := r.Cookie(googleNonceCookie)
		if err != nil || nonceCookie.Value == "" ||
			subtle.ConstantTimeCompare([]byte(nonceCookie.Value), []byte(idTokenNonce)) != 1 {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "nonce_mismatch")
			return
		}

		user, err := d.Identity.UpsertFromOAuth(r.Context(), identity.ProviderGoogle, profile)
		if err != nil {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "user_upsert_failed")
			return
		}
		creds, err := d.Auth.IssueCredentials(r.Context(), user.ID, nil)
		if err != nil {
			redirectToFrontendError(w, r, d.GoogleFrontendURL, "session_issue_failed")
			return
		}
		sellers, _ := d.Identity.ListUserSellers(r.Context(), user.ID)

		clearOAuthCookie(w, r, googleStateCookie)
		clearOAuthCookie(w, r, googleNonceCookie)

		fragment := url.Values{}
		fragment.Set("token", creds.Token)
		fragment.Set("expires_at", creds.ExpiresAt.UTC().Format(time.RFC3339))
		fragment.Set("new_user", strconv.FormatBool(len(sellers) == 0))
		http.Redirect(w, r, d.GoogleFrontendURL+"#"+fragment.Encode(), http.StatusFound)
	}
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setOAuthCookie(w http.ResponseWriter, r *http.Request, name, val string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    val,
		Path:     "/v1/auth/google",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(googleCookieTTL),
		MaxAge:   int(googleCookieTTL.Seconds()),
	})
}

func clearOAuthCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/v1/auth/google",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func redirectToFrontendError(w http.ResponseWriter, r *http.Request, frontendURL, code string) {
	if frontendURL == "" {
		http.Error(w, "oauth error: "+code, http.StatusBadRequest)
		return
	}
	sep := "#"
	if strings.Contains(frontendURL, "#") {
		sep = "&"
	}
	http.Redirect(w, r, frontendURL+sep+"error="+url.QueryEscape(code), http.StatusFound)
}
