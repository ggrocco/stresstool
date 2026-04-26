package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// Resolver resolves auth config into HTTP headers.
// For OAuth2, it uses a channel-based token cache goroutine (no mutex).
type Resolver struct {
	authCfg *config.AuthConfig

	// OAuth2 channel-based token cache
	tokenCh chan tokenRequest
	stopCh  chan struct{}
	client  *http.Client
}

type tokenRequest struct {
	eval  *placeholders.Evaluator
	reply chan tokenReply
}

type tokenReply struct {
	token string
	err   error
}

// NewResolver creates a Resolver for the given auth config.
// For OAuth2, it starts a background goroutine to cache tokens.
// Call Close() when done.
func NewResolver(authCfg *config.AuthConfig) *Resolver {
	r := &Resolver{
		authCfg: authCfg,
	}

	if authCfg != nil && authCfg.OAuth2ClientCredentials != nil {
		r.tokenCh = make(chan tokenRequest)
		r.stopCh = make(chan struct{})
		r.client = &http.Client{Timeout: 30 * time.Second}
		go r.oauth2CacheLoop()
	}

	return r
}

// Close stops the OAuth2 token cache goroutine if running.
func (r *Resolver) Close() {
	if r.stopCh != nil {
		close(r.stopCh)
	}
}

// ResolveHeaders returns the HTTP headers for the configured auth type.
// Evaluates placeholders in auth field values using the provided evaluator.
func (r *Resolver) ResolveHeaders(eval *placeholders.Evaluator) (map[string]string, error) {
	if r.authCfg == nil {
		return nil, nil
	}

	switch {
	case r.authCfg.JWT != nil:
		return r.resolveJWT(eval)
	case r.authCfg.BasicAuth != nil:
		return r.resolveBasicAuth(eval)
	case r.authCfg.Bearer != nil:
		return r.resolveBearer(eval)
	case r.authCfg.APIKey != nil:
		return r.resolveAPIKey(eval)
	case r.authCfg.OAuth2ClientCredentials != nil:
		return r.resolveOAuth2(eval)
	}

	return nil, nil
}

func (r *Resolver) resolveJWT(eval *placeholders.Evaluator) (map[string]string, error) {
	token, err := buildJWT(r.authCfg.JWT, eval)
	if err != nil {
		return nil, err
	}
	return map[string]string{"Authorization": "Bearer " + token}, nil
}

func (r *Resolver) resolveBasicAuth(eval *placeholders.Evaluator) (map[string]string, error) {
	ba := r.authCfg.BasicAuth

	username, err := eval.Evaluate(ba.Username)
	if err != nil {
		return nil, fmt.Errorf("basic_auth username: %w", err)
	}
	password, err := eval.Evaluate(ba.Password)
	if err != nil {
		return nil, fmt.Errorf("basic_auth password: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return map[string]string{"Authorization": "Basic " + encoded}, nil
}

func (r *Resolver) resolveBearer(eval *placeholders.Evaluator) (map[string]string, error) {
	token, err := eval.Evaluate(r.authCfg.Bearer.Token)
	if err != nil {
		return nil, fmt.Errorf("bearer token: %w", err)
	}

	return map[string]string{"Authorization": "Bearer " + token}, nil
}

func (r *Resolver) resolveAPIKey(eval *placeholders.Evaluator) (map[string]string, error) {
	ak := r.authCfg.APIKey

	header, err := eval.Evaluate(ak.Header)
	if err != nil {
		return nil, fmt.Errorf("api_key header: %w", err)
	}
	key, err := eval.Evaluate(ak.Key)
	if err != nil {
		return nil, fmt.Errorf("api_key key: %w", err)
	}

	return map[string]string{header: key}, nil
}

func (r *Resolver) resolveOAuth2(eval *placeholders.Evaluator) (map[string]string, error) {
	reply := make(chan tokenReply, 1)
	select {
	case r.tokenCh <- tokenRequest{eval: eval, reply: reply}:
	case <-r.stopCh:
		return nil, fmt.Errorf("oauth2: resolver closed")
	}

	select {
	case resp := <-reply:
		if resp.err != nil {
			return nil, resp.err
		}
		return map[string]string{"Authorization": "Bearer " + resp.token}, nil
	case <-r.stopCh:
		return nil, fmt.Errorf("oauth2: resolver closed")
	}
}

// oauth2CacheLoop is the goroutine that owns OAuth2 token state.
// It serves token requests via the tokenCh channel.
func (r *Resolver) oauth2CacheLoop() {
	var cachedToken string
	var expiresAt time.Time

	for {
		select {
		case <-r.stopCh:
			return
		case req := <-r.tokenCh:
			if cachedToken != "" && time.Now().Before(expiresAt) {
				req.reply <- tokenReply{token: cachedToken}
				continue
			}

			token, expiresIn, err := r.fetchOAuth2Token(req.eval)
			if err != nil {
				req.reply <- tokenReply{err: fmt.Errorf("oauth2: %w", err)}
				continue
			}

			cachedToken = token
			// Refresh 30 seconds before expiry, or at 75% of lifetime
			margin := 30 * time.Second
			if lifetime := time.Duration(expiresIn) * time.Second; lifetime*3/4 < margin {
				margin = lifetime * 3 / 4
			}
			expiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - margin)

			req.reply <- tokenReply{token: cachedToken}
		}
	}
}

func (r *Resolver) fetchOAuth2Token(eval *placeholders.Evaluator) (token string, expiresIn int64, err error) {
	o := r.authCfg.OAuth2ClientCredentials

	tokenURL, err := eval.Evaluate(o.TokenURL)
	if err != nil {
		return "", 0, fmt.Errorf("token_url: %w", err)
	}
	clientID, err := eval.Evaluate(o.ClientID)
	if err != nil {
		return "", 0, fmt.Errorf("client_id: %w", err)
	}
	clientSecret, err := eval.Evaluate(o.ClientSecret)
	if err != nil {
		return "", 0, fmt.Errorf("client_secret: %w", err)
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	if len(o.Scopes) > 0 {
		evaluatedScopes := make([]string, 0, len(o.Scopes))
		for _, s := range o.Scopes {
			es, err := eval.Evaluate(s)
			if err != nil {
				return "", 0, fmt.Errorf("scope: %w", err)
			}
			evaluatedScopes = append(evaluatedScopes, es)
		}
		data.Set("scope", strings.Join(evaluatedScopes, " "))
	}

	resp, err := r.client.PostForm(tokenURL, data)
	if err != nil {
		return "", 0, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", 0, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", 0, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", 0, fmt.Errorf("token endpoint returned empty access_token")
	}

	if tokenResp.ExpiresIn <= 0 {
		tokenResp.ExpiresIn = 3600 // default 1 hour
	}

	return tokenResp.AccessToken, tokenResp.ExpiresIn, nil
}
