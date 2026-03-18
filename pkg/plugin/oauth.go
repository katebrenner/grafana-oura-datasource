package plugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	ouraAuthorizeURL = "https://cloud.ouraring.com/oauth/authorize"
	ouraTokenURL     = "https://api.ouraring.com/oauth/token"
	// Default Oura scopes for read access to common data.
	ouraDefaultScope = "personal daily email"
)

// oauthTokenResponse matches Oura's token endpoint response.
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// BuildAuthorizeURL returns the Oura authorization URL for the authorization code flow.
// redirectURI should be the datasource config page URL (same as in Strava plugin).
func BuildAuthorizeURL(clientID, redirectURI string) string {
	return buildAuthorizeURL(clientID, redirectURI, "")
}

func buildAuthorizeURL(clientID, redirectURI, state string) string {
	u, _ := url.Parse(ouraAuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", ouraDefaultScope)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// exchangeCodeForToken exchanges the authorization code for an access token (and refresh token).
// Oura accepts client_id and client_secret via HTTP Basic Auth (recommended) or in the body.
// redirect_uri must match exactly the value used in the authorize request.
func exchangeCodeForToken(ctx context.Context, clientID, clientSecret, redirectURI, code string) (accessToken, refreshToken string, err error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ouraTokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Oura docs: "client_id as username and client_secret as password" for Basic Auth
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret)))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("token request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tok oauthTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", "", fmt.Errorf("parse token response: %w", err)
	}
	return tok.AccessToken, tok.RefreshToken, nil
}
