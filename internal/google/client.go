// Package google talks to Google APIs (YouTube live, Drive, Sheets) with OAuth 2.0 for installed apps.
// Plain REST over net/http, no Google client libraries (keeps the exe small).
package google

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	authEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	apiBase       = "https://www.googleapis.com/youtube/v3/"
	driveBase     = "https://www.googleapis.com/drive/v3/"
	uploadBase    = "https://www.googleapis.com/upload/drive/v3/"
	sheetsBase    = "https://sheets.googleapis.com/v4/"
)

// Scopes requested at sign-in.
const (
	ScopeYouTube = "https://www.googleapis.com/auth/youtube"
	// drive.file: only files this app created (backup folder, results spreadsheet); non-sensitive.
	ScopeDriveFile = "https://www.googleapis.com/auth/drive.file"
	Scopes         = ScopeYouTube + " " + ScopeDriveFile
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---------------------------------------------------------------- login (authorization code + PKCE)

// Login is one pending sign-in started from the settings page.
type Login struct {
	State    string
	Verifier string
	Redirect string
	Created  time.Time
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewLogin prepares a sign-in; redirect must be a loopback URL of this app (Google allows any port
// for loopback redirects of "Desktop app" OAuth clients).
func NewLogin(redirect string) *Login {
	return &Login{State: randomString(24), Verifier: randomString(48), Redirect: redirect, Created: time.Now()}
}

// AuthURL is the Google consent page the user opens in the browser.
func (l *Login) AuthURL(clientID string) string {
	sum := sha256.Sum256([]byte(l.Verifier))
	q := url.Values{
		"client_id":              {clientID},
		"redirect_uri":           {l.Redirect},
		"response_type":          {"code"},
		"scope":                  {Scopes},
		"include_granted_scopes": {"true"},
		"access_type":            {"offline"},
		"prompt":                 {"consent select_account"},
		"state":                  {l.State},
		"code_challenge":         {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method":  {"S256"},
	}
	return authEndpoint + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func postToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	var tr tokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tr, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpClient.Do(req)
	if err != nil {
		return tr, fmt.Errorf("Google nedostupný: %w", err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return tr, fmt.Errorf("Google: nečitelná odpověď (%s)", res.Status)
	}
	if tr.Error != "" {
		return tr, fmt.Errorf("Google: %s %s", tr.Error, tr.ErrorDesc)
	}
	return tr, nil
}

// Exchange trades the code from the redirect for a refresh token; scopes are what the user granted
// (the consent screen lets them untick some).
func (l *Login) Exchange(ctx context.Context, clientID, clientSecret, code string) (refreshToken, scopes string, err error) {
	tr, err := postToken(ctx, url.Values{
		"client_id": {clientID}, "client_secret": {clientSecret},
		"code": {code}, "code_verifier": {l.Verifier},
		"grant_type": {"authorization_code"}, "redirect_uri": {l.Redirect},
	})
	if err != nil {
		return "", "", err
	}
	if tr.RefreshToken == "" {
		return "", "", errors.New("Google nevrátil refresh token")
	}
	return tr.RefreshToken, tr.Scope, nil
}

// HasScope reports whether a space-separated scope list contains s.
func HasScope(list, s string) bool {
	for _, x := range strings.Fields(list) {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- API client

// Client calls the API on behalf of the signed-in channel; the access token is refreshed as needed.
type Client struct {
	ClientID, ClientSecret, RefreshToken string

	mu      sync.Mutex
	access  string
	expires time.Time
}

// ErrAuth means the sign-in is no longer valid (revoked, expired): the user must sign in again.
var ErrAuth = errors.New("přihlášení ke Google účtu vypršelo nebo bylo zrušeno – přihlaste se znovu v Nastavení")

func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && time.Until(c.expires) > time.Minute {
		return c.access, nil
	}
	tr, err := postToken(ctx, url.Values{
		"client_id": {c.ClientID}, "client_secret": {c.ClientSecret},
		"refresh_token": {c.RefreshToken}, "grant_type": {"refresh_token"},
	})
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			return "", ErrAuth
		}
		return "", err
	}
	c.access, c.expires = tr.AccessToken, time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second)
	return c.access, nil
}

// APIError is an error response from the YouTube API.
type APIError struct {
	Status  int
	Reason  string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Google API %d %s: %s", e.Status, e.Reason, e.Message)
}

func isNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// call: JSON request to the YouTube Data API.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var rd io.Reader
	ct := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd, ct = bytes.NewReader(b), "application/json"
	}
	return c.do(ctx, method, apiBase+path+"?"+q.Encode(), ct, rd, out)
}

// do sends an authorized request to any Google API URL; out (if not nil) receives the JSON answer.
func (c *Client) do(ctx context.Context, method, fullURL, contentType string, body io.Reader, out any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Google nedostupný: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Errors  []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		ae := &APIError{Status: res.StatusCode, Message: e.Error.Message}
		if len(e.Error.Errors) > 0 {
			ae.Reason = e.Error.Errors[0].Reason
		}
		if res.StatusCode == http.StatusUnauthorized {
			return ErrAuth
		}
		return ae
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = data
		return nil
	}
	return json.Unmarshal(data, out)
}
