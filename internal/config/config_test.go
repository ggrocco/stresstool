package config

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateAuth_SingleType(t *testing.T) {
	cfg := &Config{
		Auth: &AuthConfig{
			BasicAuth: &BasicAuthConfig{
				Username: "admin",
				Password: "secret",
			},
		},
		Tests: []Test{{
			Path: "/x", RequestsPerSecond: 1, Threads: 1, RunSeconds: 1,
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAuth_MultipleTypes(t *testing.T) {
	cfg := &Config{
		Auth: &AuthConfig{
			BasicAuth: &BasicAuthConfig{Username: "a", Password: "b"},
			Bearer:    &BearerAuthConfig{Token: "t"},
		},
		Tests: []Test{{
			Path: "/x", RequestsPerSecond: 1, Threads: 1, RunSeconds: 1,
		}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for multiple auth types")
	}
	if !strings.Contains(err.Error(), "only one auth type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAuth_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		auth *AuthConfig
		want string
	}{
		{"basic_auth no username", &AuthConfig{BasicAuth: &BasicAuthConfig{Password: "p"}}, "username is required"},
		{"basic_auth no password", &AuthConfig{BasicAuth: &BasicAuthConfig{Username: "u"}}, "password is required"},
		{"bearer no token", &AuthConfig{Bearer: &BearerAuthConfig{}}, "token is required"},
		{"api_key no header", &AuthConfig{APIKey: &APIKeyAuthConfig{Key: "k"}}, "header is required"},
		{"api_key no key", &AuthConfig{APIKey: &APIKeyAuthConfig{Header: "h"}}, "key is required"},
		{"oauth2 no token_url", &AuthConfig{OAuth2ClientCredentials: &OAuth2ClientCredentialsConfig{ClientID: "c", ClientSecret: "s"}}, "token_url is required"},
		{"oauth2 no client_id", &AuthConfig{OAuth2ClientCredentials: &OAuth2ClientCredentialsConfig{TokenURL: "u", ClientSecret: "s"}}, "client_id is required"},
		{"oauth2 no client_secret", &AuthConfig{OAuth2ClientCredentials: &OAuth2ClientCredentialsConfig{TokenURL: "u", ClientID: "c"}}, "client_secret is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Auth: tc.auth,
				Tests: []Test{{
					Path: "/x", RequestsPerSecond: 1, Threads: 1, RunSeconds: 1,
				}},
			}
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateAuth_AuthorizationHeaderConflict(t *testing.T) {
	cfg := &Config{
		Auth: &AuthConfig{
			BasicAuth: &BasicAuthConfig{Username: "u", Password: "p"},
		},
		Tests: []Test{{
			Path:              "/x",
			RequestsPerSecond: 1,
			Threads:           1,
			RunSeconds:        1,
			Headers:           map[string]string{"Authorization": "Bearer manual"},
		}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for Authorization header conflict")
	}
	if !strings.Contains(err.Error(), "cannot set Authorization header") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAuth_OptOutAllowsAuthorizationHeader(t *testing.T) {
	f := false
	cfg := &Config{
		Auth: &AuthConfig{
			BasicAuth: &BasicAuthConfig{Username: "u", Password: "p"},
		},
		Tests: []Test{{
			Path:              "/x",
			RequestsPerSecond: 1,
			Threads:           1,
			RunSeconds:        1,
			Auth:              &f,
			Headers:           map[string]string{"Authorization": "Bearer manual"},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("test with auth:false should allow Authorization header: %v", err)
	}
}

func TestValidateAuth_NoAuthDefined(t *testing.T) {
	cfg := &Config{
		Tests: []Test{{
			Path: "/x", RequestsPerSecond: 1, Threads: 1, RunSeconds: 1,
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("no auth should be valid: %v", err)
	}
}

func TestAuthType(t *testing.T) {
	tests := []struct {
		name string
		auth *AuthConfig
		want string
	}{
		{"nil", nil, ""},
		{"basic", &AuthConfig{BasicAuth: &BasicAuthConfig{Username: "u", Password: "p"}}, "basic_auth"},
		{"bearer", &AuthConfig{Bearer: &BearerAuthConfig{Token: "t"}}, "bearer"},
		{"api_key", &AuthConfig{APIKey: &APIKeyAuthConfig{Header: "h", Key: "k"}}, "api_key"},
		{"oauth2", &AuthConfig{OAuth2ClientCredentials: &OAuth2ClientCredentialsConfig{TokenURL: "u", ClientID: "c", ClientSecret: "s"}}, "oauth2_client_credentials"},
		{"empty", &AuthConfig{}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.auth.AuthType()
			if got != tc.want {
				t.Fatalf("AuthType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseYAMLAuth(t *testing.T) {
	yaml := `
auth:
  basic_auth:
    username: admin
    password: secret123
tests:
  - name: test1
    path: /api
    requests_per_second: 1
    threads: 1
    run_seconds: 1
  - name: test2
    path: /public
    auth: false
    requests_per_second: 1
    threads: 1
    run_seconds: 1
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth == nil || cfg.Auth.BasicAuth == nil {
		t.Fatal("expected basic_auth to be parsed")
	}
	if cfg.Auth.BasicAuth.Username != "admin" {
		t.Fatalf("username = %q", cfg.Auth.BasicAuth.Username)
	}
	if cfg.Auth.BasicAuth.Password != "secret123" {
		t.Fatalf("password = %q", cfg.Auth.BasicAuth.Password)
	}

	if len(cfg.Tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(cfg.Tests))
	}

	// test1: auth not set (nil) → uses config auth
	if cfg.Tests[0].Auth != nil {
		t.Fatal("test1.Auth should be nil")
	}

	// test2: auth: false → disabled
	if cfg.Tests[1].Auth == nil || *cfg.Tests[1].Auth != false {
		t.Fatal("test2.Auth should be false")
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

func TestExecuteFunc(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := &FuncDef{
			Name: "echo",
			Cmd:  []string{"echo", "-n", "ok"},
		}
		got, err := f.ExecuteFunc()
		if err != nil {
			t.Fatalf("ExecuteFunc() error = %v", err)
		}
		if got != "ok" {
			t.Fatalf("ExecuteFunc() = %q, want ok", got)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping timeout test in short mode")
		}
		if runtime.GOOS == "windows" {
			t.Skip("requires sleep command")
		}

		start := time.Now()
		f := &FuncDef{
			Name: "sleep",
			Cmd:  []string{"sleep", "5"},
		}
		_, err := f.ExecuteFunc()
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("unexpected error: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 4*time.Second {
			t.Fatalf("timeout should happen around 3s, took %s", elapsed)
		}
	})
}
