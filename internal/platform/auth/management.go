package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// managementHTTPTimeout bounds each call to the Auth0 Management API.
const managementHTTPTimeout = 10 * time.Second

// Auth0Management deletes users at Auth0 through the Management API, as the
// machine to machine application of the tenant (client credentials grant,
// permission delete:users only). The client secret never appears in an
// error or a log.
type Auth0Management struct {
	baseURL      string
	clientID     string
	clientSecret string
	http         *http.Client
}

// NewAuth0Management returns the Management API client of the tenant domain
// (host name only).
func NewAuth0Management(domain, clientID, clientSecret string) *Auth0Management {
	return newManagement("https://"+domain, clientID, clientSecret)
}

// newManagement takes the base URL, so that tests can use a local server.
func newManagement(baseURL, clientID, clientSecret string) *Auth0Management {
	return &Auth0Management{
		baseURL:      baseURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		http: &http.Client{
			Timeout: managementHTTPTimeout,
			// Never followed: a redirect would send the client secret (body
			// of a 307) or the token to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// DeleteUser deletes the Auth0 user of subject (the "sub" of its tokens). A
// user Auth0 no longer knows counts as deleted. A token is requested for
// every call: deletions are rare, nothing is cached.
func (m *Auth0Management) DeleteUser(ctx context.Context, subject string) error {
	token, err := m.token(ctx)
	if err != nil {
		return fmt.Errorf("auth0 delete user: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.baseURL+"/api/v2/users/"+url.PathEscape(subject), nil)
	if err != nil {
		return fmt.Errorf("auth0 delete user: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("auth0 delete user: %w", withoutURL(err))
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("auth0 delete user: status %d", resp.StatusCode)
	}
}

// token returns an access token of the Management API (client credentials).
func (m *Auth0Management) token(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {m.clientID},
		"client_secret": {m.clientSecret},
		"audience":      {m.baseURL + "/api/v2/"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.http.Do(req)
	if err != nil {
		// *url.Error names the URL only: the secret is in the body.
		return "", fmt.Errorf("token request: %w", err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request: status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", fmt.Errorf("token response: %w", err)
	}
	if body.AccessToken == "" {
		return "", errors.New("token response: no access token")
	}
	return body.AccessToken, nil
}

// withoutURL drops the URL from an error of http.Client.Do: the URL of a
// user carries its subject, which never goes to a log.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// drain reads what is left of a response body, bounded, and closes it, so
// the connection can be reused.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}
