package config

import (
	"strings"
	"testing"
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
		{"jwt no signature", &AuthConfig{JWT: &JWTAuthConfig{}}, "signature is required"},
		{"jwt no secret", &AuthConfig{JWT: &JWTAuthConfig{Signature: &JWTSignatureConfig{}}}, "signature.secret is required"},
		{"jwt bad alg", &AuthConfig{JWT: &JWTAuthConfig{
			Header:    map[string]string{"alg": "RS256"},
			Signature: &JWTSignatureConfig{Secret: "s"},
		}}, "unsupported alg"},
		{"jwt negative ttl", &AuthConfig{JWT: &JWTAuthConfig{
			Signature:  &JWTSignatureConfig{Secret: "s"},
			TTLSeconds: -1,
		}}, "ttl_seconds must be >= 0"},
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
		{"jwt", &AuthConfig{JWT: &JWTAuthConfig{Signature: &JWTSignatureConfig{Secret: "s"}}}, "jwt"},
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

func TestParseYAMLJWT(t *testing.T) {
	yaml := `
auth:
  jwt:
    header:
      alg: HS256
      typ: JWT
      kid: my-key
    payload:
      iss: stresstool
      sub: load-test-user
      aud: my-api
    signature:
      secret: super-secret
    ttl_seconds: 600
tests:
  - name: t
    path: /api
    requests_per_second: 1
    threads: 1
    run_seconds: 1
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth == nil || cfg.Auth.JWT == nil {
		t.Fatal("expected jwt to be parsed")
	}
	jwt := cfg.Auth.JWT
	if jwt.Header["alg"] != "HS256" {
		t.Errorf("header.alg = %v", jwt.Header["alg"])
	}
	if jwt.Header["kid"] != "my-key" {
		t.Errorf("header.kid = %v", jwt.Header["kid"])
	}
	if jwt.Payload["iss"] != "stresstool" {
		t.Errorf("payload.iss = %v", jwt.Payload["iss"])
	}
	if jwt.Signature == nil || jwt.Signature.Secret != "super-secret" {
		t.Errorf("signature secret = %v", jwt.Signature)
	}
	if jwt.TTLSeconds != 600 {
		t.Errorf("ttl_seconds = %d, want 600", jwt.TTLSeconds)
	}
	if cfg.Auth.AuthType() != "jwt" {
		t.Errorf("AuthType = %q, want jwt", cfg.Auth.AuthType())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

func TestValidateSteps_OK(t *testing.T) {
	cfg := &Config{
		Tests: []Test{{
			Name:              "flow",
			RequestsPerSecond: 1,
			Threads:           1,
			RunSeconds:        1,
			Steps: []Step{
				{Name: "s1", Path: "/a"},
				{Name: "s2", Path: "/b", Method: "POST"},
			},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Tests[0].Steps[0].Method != "GET" {
		t.Fatalf("expected default method GET on step 0, got %q", cfg.Tests[0].Steps[0].Method)
	}
	if cfg.Tests[0].Steps[1].Method != "POST" {
		t.Fatalf("expected POST preserved on step 1, got %q", cfg.Tests[0].Steps[1].Method)
	}
}

func TestValidateSteps_MutualExclusion(t *testing.T) {
	cfg := &Config{
		Tests: []Test{{
			Name:              "flow",
			Path:              "/top",
			RequestsPerSecond: 1,
			Threads:           1,
			RunSeconds:        1,
			Steps:             []Step{{Path: "/a"}},
		}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "steps are defined") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestValidateSteps_PathRequired(t *testing.T) {
	cfg := &Config{
		Tests: []Test{{
			Name:              "flow",
			RequestsPerSecond: 1,
			Threads:           1,
			RunSeconds:        1,
			Steps:             []Step{{Name: "missing"}},
		}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected step path error, got %v", err)
	}
}
