package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

func newEval() *placeholders.Evaluator {
	return placeholders.NewEvaluator(&config.Config{})
}

func TestResolveBasicAuth(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		BasicAuth: &config.BasicAuthConfig{
			Username: "admin",
			Password: "secret",
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:secret"))
	if headers["Authorization"] != expected {
		t.Fatalf("got %q, want %q", headers["Authorization"], expected)
	}
}

func TestResolveBearer(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		Bearer: &config.BearerAuthConfig{
			Token: "my-token-123",
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	if headers["Authorization"] != "Bearer my-token-123" {
		t.Fatalf("got %q", headers["Authorization"])
	}
}

func TestResolveAPIKey(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		APIKey: &config.APIKeyAuthConfig{
			Header: "X-API-Key",
			Key:    "secret-key-456",
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	if headers["X-API-Key"] != "secret-key-456" {
		t.Fatalf("got %q", headers["X-API-Key"])
	}
}

func TestResolveOAuth2(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		if r.FormValue("client_id") != "my-client" {
			t.Errorf("client_id = %q", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "my-secret" {
			t.Errorf("client_secret = %q", r.FormValue("client_secret"))
		}
		if r.FormValue("scope") != "read write" {
			t.Errorf("scope = %q", r.FormValue("scope"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "oauth-token-abc",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
	defer ts.Close()

	r := NewResolver(&config.AuthConfig{
		OAuth2ClientCredentials: &config.OAuth2ClientCredentialsConfig{
			TokenURL:     ts.URL,
			ClientID:     "my-client",
			ClientSecret: "my-secret",
			Scopes:       []string{"read", "write"},
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	if headers["Authorization"] != "Bearer oauth-token-abc" {
		t.Fatalf("got %q", headers["Authorization"])
	}
}

func TestResolveOAuth2_CachedToken(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": fmt.Sprintf("token-%d", callCount),
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
	defer ts.Close()

	r := NewResolver(&config.AuthConfig{
		OAuth2ClientCredentials: &config.OAuth2ClientCredentialsConfig{
			TokenURL:     ts.URL,
			ClientID:     "c",
			ClientSecret: "s",
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	// First call fetches
	h1, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	// Second call should use cache
	h2, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	if h1["Authorization"] != h2["Authorization"] {
		t.Fatalf("expected cached token, got %q then %q", h1["Authorization"], h2["Authorization"])
	}
	if callCount != 1 {
		t.Fatalf("expected 1 token fetch, got %d", callCount)
	}
}

func TestResolveNilAuth(t *testing.T) {
	r := NewResolver(nil)
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	if headers != nil {
		t.Fatalf("expected nil, got %v", headers)
	}
}

func TestResolveWithPlaceholders(t *testing.T) {
	cfg := &config.Config{
		Funcs: []config.FuncDef{
			{Name: "get_pass", Cmd: []string{"echo", "dynamic-pass"}},
		},
	}
	eval := placeholders.NewEvaluator(cfg)
	defer eval.Close()

	r := NewResolver(&config.AuthConfig{
		BasicAuth: &config.BasicAuthConfig{
			Username: "admin",
			Password: "{{ get_pass() }}",
		},
	})
	defer r.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:dynamic-pass"))
	if headers["Authorization"] != expected {
		t.Fatalf("got %q, want %q", headers["Authorization"], expected)
	}
}

func TestResolveOAuth2_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid_client"))
	}))
	defer ts.Close()

	r := NewResolver(&config.AuthConfig{
		OAuth2ClientCredentials: &config.OAuth2ClientCredentialsConfig{
			TokenURL:     ts.URL,
			ClientID:     "bad",
			ClientSecret: "bad",
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	_, err := r.ResolveHeaders(eval)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected status code in error, got: %v", err)
	}
}
