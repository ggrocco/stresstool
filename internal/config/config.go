package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level configuration structure
type Config struct {
	Funcs []FuncDef   `yaml:"funcs"`
	Auth  *AuthConfig `yaml:"auth,omitempty"`
	Tests []Test      `yaml:"tests"`
}

// AuthConfig holds auth configuration keyed by type. Only one type may be set.
//
// JWT is the recommended and most widely-used credential type; it is listed
// first so that tooling and docs surface it as the default choice.
type AuthConfig struct {
	JWT                     *JWTAuthConfig                 `yaml:"jwt,omitempty"`
	BasicAuth               *BasicAuthConfig               `yaml:"basic_auth,omitempty"`
	Bearer                  *BearerAuthConfig              `yaml:"bearer,omitempty"`
	APIKey                  *APIKeyAuthConfig              `yaml:"api_key,omitempty"`
	OAuth2ClientCredentials *OAuth2ClientCredentialsConfig `yaml:"oauth2_client_credentials,omitempty"`
}

// AuthType returns which auth type is configured, or "" if none.
func (a *AuthConfig) AuthType() string {
	if a == nil {
		return ""
	}
	if a.JWT != nil {
		return "jwt"
	}
	if a.BasicAuth != nil {
		return "basic_auth"
	}
	if a.Bearer != nil {
		return "bearer"
	}
	if a.APIKey != nil {
		return "api_key"
	}
	if a.OAuth2ClientCredentials != nil {
		return "oauth2_client_credentials"
	}
	return ""
}

// JWTAuthConfig holds JWT (JSON Web Token) authentication configuration.
//
// The token is assembled from three blocks: header, payload, and signature.
// User-provided Header and Payload values are merged on top of sensible
// defaults, so every field can be overridden but nothing is required:
//
//   - Header defaults: {"alg": "HS256", "typ": "JWT"}
//   - Payload defaults: {"iat": <now-unix>, "exp": <now+ttl-unix>}
//
// All values are flat string key/value pairs. Numeric claims like "exp" and
// "iat" are coerced to JSON numbers automatically when the token is built.
// The signing algorithm is read from Header["alg"] (supported: HS256, HS384,
// HS512). Values are evaluated as placeholders, so dynamic claims like
// {{ uuid() }} or {{ now() }} work the same as in other auth types.
type JWTAuthConfig struct {
	// Header is the JWT header key/value pairs; merged on top of the defaults.
	Header map[string]string `yaml:"header,omitempty"`
	// Payload is the JWT claims key/value pairs; merged on top of the defaults.
	Payload map[string]string `yaml:"payload,omitempty"`
	// Signature holds the signing material for the algorithm in Header["alg"].
	Signature *JWTSignatureConfig `yaml:"signature,omitempty"`
	// TTLSeconds is the default "exp" lifetime in seconds (default: 3600).
	// Only used if the user does not override "exp" in Payload.
	TTLSeconds int `yaml:"ttl_seconds,omitempty"`
}

// JWTSignatureConfig holds signing material for a JWT.
// For HMAC algorithms (HS256/HS384/HS512), only Secret is required.
type JWTSignatureConfig struct {
	// Secret is the shared secret for HMAC signing algorithms.
	Secret string `yaml:"secret,omitempty"`
}

// BasicAuthConfig holds basic authentication credentials.
type BasicAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// BearerAuthConfig holds a bearer token.
type BearerAuthConfig struct {
	Token string `yaml:"token"`
}

// APIKeyAuthConfig holds an API key sent as a custom header.
type APIKeyAuthConfig struct {
	Header string `yaml:"header"`
	Key    string `yaml:"key"`
}

// OAuth2ClientCredentialsConfig holds OAuth2 client credentials grant parameters.
type OAuth2ClientCredentialsConfig struct {
	TokenURL     string   `yaml:"token_url"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes,omitempty"`
}

// FuncDef defines a custom function that can be called via placeholders
type FuncDef struct {
	Name string   `yaml:"name"`
	Cmd  []string `yaml:"cmd"`
}

// Test defines a single HTTP stress test
type Test struct {
	Name              string            `yaml:"name"`
	Path              string            `yaml:"path"`
	Method            string            `yaml:"method"`
	RequestsPerSecond int               `yaml:"requests_per_second"`
	Threads           int               `yaml:"threads"`
	RunSeconds        int               `yaml:"run_seconds"`
	Headers           map[string]string `yaml:"headers"`
	Body              string            `yaml:"body"`
	Assert            *Assertion        `yaml:"assert"`
	Nodes             map[string]Node   `yaml:"nodes"`
	Auth              *bool             `yaml:"auth,omitempty"` // nil=use config auth, false=disable
}

// Node allows overriding settings for a specific node name
type Node struct {
	RequestsPerSecond int `yaml:"requests_per_second"`
	Threads           int `yaml:"threads"`
}

// Assertion defines what to check in responses
type Assertion struct {
	StatusCode    int    `yaml:"status_code"`
	BodyContains  string `yaml:"body_contains"`
	BodyEquals    string `yaml:"body_equals"`
	BodyNotEquals string `yaml:"body_not_equals"`
	MaxLatencyMs  int    `yaml:"max_latency_ms"`
}

// LoadConfig reads and parses a YAML configuration file
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return ParseConfig(data)
}

// ParseConfig parses a YAML configuration from raw bytes
func ParseConfig(data []byte) (*Config, error) {
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &config, nil
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	// Validate auth config
	if err := c.Auth.validate(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	// Check for duplicate func names
	funcNames := make(map[string]bool)
	for _, f := range c.Funcs {
		if f.Name == "" {
			return fmt.Errorf("func name cannot be empty")
		}
		if funcNames[f.Name] {
			return fmt.Errorf("duplicate func name: %s", f.Name)
		}
		if len(f.Cmd) == 0 {
			return fmt.Errorf("func %s: cmd cannot be empty", f.Name)
		}
		funcNames[f.Name] = true
	}

	hasAuth := c.Auth != nil && c.Auth.AuthType() != ""

	// Validate each test
	for i, test := range c.Tests {
		if test.Path == "" {
			return fmt.Errorf("test[%d]: path is required", i)
		}
		if test.RequestsPerSecond <= 0 {
			return fmt.Errorf("test[%d]: requests_per_second must be > 0", i)
		}
		if test.Threads <= 0 {
			return fmt.Errorf("test[%d]: threads must be > 0", i)
		}
		if test.RunSeconds <= 0 {
			return fmt.Errorf("test[%d]: run_seconds must be > 0", i)
		}
		if test.Method == "" {
			c.Tests[i].Method = "GET"
		}

		// Check for conflict: auth defined + test uses auth + test has Authorization header
		authEnabled := test.Auth == nil || *test.Auth
		if hasAuth && authEnabled {
			if _, ok := test.Headers["Authorization"]; ok {
				return fmt.Errorf("test[%d]: cannot set Authorization header when auth is configured; use auth: false to disable", i)
			}
		}

		for nodeName, nodeCfg := range test.Nodes {
			if nodeCfg.RequestsPerSecond < 0 {
				return fmt.Errorf("test[%d].nodes[%s]: requests_per_second must be >= 0", i, nodeName)
			}
			if nodeCfg.Threads < 0 {
				return fmt.Errorf("test[%d].nodes[%s]: threads must be >= 0", i, nodeName)
			}
		}
	}

	return nil
}

// validate checks that the auth configuration is valid (exactly one type, required fields present).
func (a *AuthConfig) validate() error {
	if a == nil {
		return nil
	}

	count := 0
	if a.JWT != nil {
		count++
	}
	if a.BasicAuth != nil {
		count++
	}
	if a.Bearer != nil {
		count++
	}
	if a.APIKey != nil {
		count++
	}
	if a.OAuth2ClientCredentials != nil {
		count++
	}

	if count == 0 {
		return nil
	}
	if count > 1 {
		return fmt.Errorf("only one auth type can be configured, found %d", count)
	}

	switch {
	case a.JWT != nil:
		if a.JWT.Signature == nil {
			return fmt.Errorf("jwt: signature is required")
		}
		alg := jwtAlg(a.JWT.Header)
		switch alg {
		case "HS256", "HS384", "HS512":
			if a.JWT.Signature.Secret == "" {
				return fmt.Errorf("jwt: signature.secret is required for %s", alg)
			}
		default:
			return fmt.Errorf("jwt: unsupported alg %q (supported: HS256, HS384, HS512)", alg)
		}
		if a.JWT.TTLSeconds < 0 {
			return fmt.Errorf("jwt: ttl_seconds must be >= 0")
		}
	case a.BasicAuth != nil:
		if a.BasicAuth.Username == "" {
			return fmt.Errorf("basic_auth: username is required")
		}
		if a.BasicAuth.Password == "" {
			return fmt.Errorf("basic_auth: password is required")
		}
	case a.Bearer != nil:
		if a.Bearer.Token == "" {
			return fmt.Errorf("bearer: token is required")
		}
	case a.APIKey != nil:
		if a.APIKey.Header == "" {
			return fmt.Errorf("api_key: header is required")
		}
		if a.APIKey.Key == "" {
			return fmt.Errorf("api_key: key is required")
		}
	case a.OAuth2ClientCredentials != nil:
		o := a.OAuth2ClientCredentials
		if o.TokenURL == "" {
			return fmt.Errorf("oauth2_client_credentials: token_url is required")
		}
		if o.ClientID == "" {
			return fmt.Errorf("oauth2_client_credentials: client_id is required")
		}
		if o.ClientSecret == "" {
			return fmt.Errorf("oauth2_client_credentials: client_secret is required")
		}
	}

	return nil
}

// jwtAlg returns the "alg" value from a JWT header map, falling back to the
// default HS256 when not set.
func jwtAlg(header map[string]string) string {
	const defaultAlg = "HS256"
	if s, ok := header["alg"]; ok && s != "" {
		return strings.ToUpper(s)
	}
	return defaultAlg
}

// GetFunc returns a function definition by name
func (c *Config) GetFunc(name string) *FuncDef {
	for _, f := range c.Funcs {
		if f.Name == name {
			return &f
		}
	}
	return nil
}

// ExecuteFunc runs a custom function command and returns its stdout
func (f *FuncDef) ExecuteFunc() (string, error) {
	if len(f.Cmd) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := exec.Command(f.Cmd[0], f.Cmd[1:]...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to execute %s: %w", f.Name, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// WithNodeOverrides returns a copy of the config with node-specific overrides applied
func (c *Config) WithNodeOverrides(nodeName string) *Config {
	if nodeName == "" {
		return c
	}

	newConfig := *c
	newConfig.Tests = make([]Test, len(c.Tests))

	for i, test := range c.Tests {
		updated := test
		if nodeCfg, ok := test.Nodes[nodeName]; ok {
			if nodeCfg.Threads > 0 {
				updated.Threads = nodeCfg.Threads
			}
			if nodeCfg.RequestsPerSecond > 0 {
				updated.RequestsPerSecond = nodeCfg.RequestsPerSecond
			}
		}
		newConfig.Tests[i] = updated
	}

	return &newConfig
}
