package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"hash"
	"strings"
	"testing"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// decodeJWT splits a compact JWT into its three parts, verifies the HMAC
// signature with the given secret, and returns the decoded header and
// payload maps. It is test-only and intentionally separate from the
// production signing code so that we catch regressions in either direction.
func decodeJWT(t *testing.T, token, secret string) (header, payload map[string]any) {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt: expected 3 parts, got %d: %s", len(parts), token)
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("jwt: decode header: %v", err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("jwt: decode payload: %v", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("jwt: decode signature: %v", err)
	}

	if err := json.Unmarshal(headerBytes, &header); err != nil {
		t.Fatalf("jwt: unmarshal header: %v", err)
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("jwt: unmarshal payload: %v", err)
	}

	alg, _ := header["alg"].(string)
	var h hash.Hash
	switch strings.ToUpper(alg) {
	case "HS256":
		h = hmac.New(sha256.New, []byte(secret))
	case "HS384":
		h = hmac.New(sha512.New384, []byte(secret))
	case "HS512":
		h = hmac.New(sha512.New, []byte(secret))
	default:
		t.Fatalf("jwt: unsupported alg %q", alg)
	}
	h.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(h.Sum(nil), sigBytes) {
		t.Fatalf("jwt: signature mismatch")
	}
	return header, payload
}

func TestResolveJWT_DefaultsApplied(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			Signature: &config.JWTSignatureConfig{Secret: "my-secret"},
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	before := time.Now().Unix()
	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().Unix()

	authz := headers["Authorization"]
	if !strings.HasPrefix(authz, "Bearer ") {
		t.Fatalf("expected Bearer prefix, got %q", authz)
	}
	token := strings.TrimPrefix(authz, "Bearer ")

	header, payload := decodeJWT(t, token, "my-secret")

	// Header defaults
	if got := header["alg"]; got != "HS256" {
		t.Errorf("header.alg = %v, want HS256", got)
	}
	if got := header["typ"]; got != "JWT" {
		t.Errorf("header.typ = %v, want JWT", got)
	}

	// Payload defaults
	iat, ok := payload["iat"].(float64)
	if !ok {
		t.Fatalf("payload.iat type = %T, want float64", payload["iat"])
	}
	if int64(iat) < before || int64(iat) > after {
		t.Errorf("payload.iat = %d, want in [%d,%d]", int64(iat), before, after)
	}
	exp, ok := payload["exp"].(float64)
	if !ok {
		t.Fatalf("payload.exp type = %T, want float64", payload["exp"])
	}
	if int64(exp)-int64(iat) != defaultJWTTTLSeconds {
		t.Errorf("payload.exp - iat = %d, want %d", int64(exp)-int64(iat), defaultJWTTTLSeconds)
	}
}

func TestResolveJWT_UserOverridesMergedOnTopOfDefaults(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			Header: map[string]any{
				// Override alg; typ should keep default value.
				"alg": "HS384",
				// Add a new custom header field.
				"kid": "key-1",
			},
			Payload: map[string]any{
				// Override exp with an explicit value; iat should keep default.
				"exp": int64(9999999999),
				"sub": "user-42",
				"iss": "stresstool",
			},
			Signature: &config.JWTSignatureConfig{Secret: "abc"},
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(headers["Authorization"], "Bearer ")
	header, payload := decodeJWT(t, token, "abc")

	if header["alg"] != "HS384" {
		t.Errorf("header.alg = %v, want HS384", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("header.typ = %v, want JWT (default)", header["typ"])
	}
	if header["kid"] != "key-1" {
		t.Errorf("header.kid = %v, want key-1", header["kid"])
	}

	if payload["sub"] != "user-42" {
		t.Errorf("payload.sub = %v, want user-42", payload["sub"])
	}
	if payload["iss"] != "stresstool" {
		t.Errorf("payload.iss = %v, want stresstool", payload["iss"])
	}
	if int64(payload["exp"].(float64)) != 9999999999 {
		t.Errorf("payload.exp = %v, want 9999999999", payload["exp"])
	}
	if _, ok := payload["iat"]; !ok {
		t.Errorf("payload.iat missing (default should remain)")
	}
}

func TestResolveJWT_HS512(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			Header:    map[string]any{"alg": "HS512"},
			Signature: &config.JWTSignatureConfig{Secret: "long-secret-for-sha-512"},
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(headers["Authorization"], "Bearer ")
	header, _ := decodeJWT(t, token, "long-secret-for-sha-512")
	if header["alg"] != "HS512" {
		t.Errorf("header.alg = %v, want HS512", header["alg"])
	}
}

func TestResolveJWT_PlaceholdersEvaluated(t *testing.T) {
	cfg := &config.Config{
		Funcs: []config.FuncDef{
			{Name: "get_secret", Cmd: []string{"echo", "shh"}},
			{Name: "get_sub", Cmd: []string{"echo", "user-from-func"}},
		},
	}
	eval := placeholders.NewEvaluator(cfg)
	defer eval.Close()

	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			Payload: map[string]any{
				"sub": "{{ get_sub() }}",
				"jti": "{{ uuid() }}",
			},
			Signature: &config.JWTSignatureConfig{Secret: "{{ get_secret() }}"},
		},
	})
	defer r.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(headers["Authorization"], "Bearer ")
	_, payload := decodeJWT(t, token, "shh")

	if payload["sub"] != "user-from-func" {
		t.Errorf("payload.sub = %v, want user-from-func", payload["sub"])
	}
	jti, ok := payload["jti"].(string)
	if !ok || jti == "" || strings.Contains(jti, "{{") {
		t.Errorf("payload.jti = %v, expected evaluated uuid", payload["jti"])
	}
}

func TestResolveJWT_CustomTTLUsed(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			TTLSeconds: 60,
			Signature:  &config.JWTSignatureConfig{Secret: "s"},
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(headers["Authorization"], "Bearer ")
	_, payload := decodeJWT(t, token, "s")
	iat := int64(payload["iat"].(float64))
	exp := int64(payload["exp"].(float64))
	if exp-iat != 60 {
		t.Errorf("exp - iat = %d, want 60", exp-iat)
	}
}

func TestResolveJWT_MissingSignatureSecret(t *testing.T) {
	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			Signature: &config.JWTSignatureConfig{Secret: ""},
		},
	})
	defer r.Close()

	eval := newEval()
	defer eval.Close()

	_, err := r.ResolveHeaders(eval)
	if err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestResolveJWT_NestedMapValuesEvaluated(t *testing.T) {
	cfg := &config.Config{
		Funcs: []config.FuncDef{
			{Name: "get_tenant", Cmd: []string{"echo", "acme"}},
		},
	}
	eval := placeholders.NewEvaluator(cfg)
	defer eval.Close()

	r := NewResolver(&config.AuthConfig{
		JWT: &config.JWTAuthConfig{
			Payload: map[string]any{
				"ctx": map[string]any{
					"tenant": "{{ get_tenant() }}",
					"roles":  []any{"admin", "{{ get_tenant() }}-owner"},
				},
			},
			Signature: &config.JWTSignatureConfig{Secret: "s"},
		},
	})
	defer r.Close()

	headers, err := r.ResolveHeaders(eval)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(headers["Authorization"], "Bearer ")
	_, payload := decodeJWT(t, token, "s")

	ctx, ok := payload["ctx"].(map[string]any)
	if !ok {
		t.Fatalf("payload.ctx type = %T, want map", payload["ctx"])
	}
	if ctx["tenant"] != "acme" {
		t.Errorf("ctx.tenant = %v, want acme", ctx["tenant"])
	}
	roles, ok := ctx["roles"].([]any)
	if !ok {
		t.Fatalf("ctx.roles type = %T, want []any", ctx["roles"])
	}
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "acme-owner" {
		t.Errorf("ctx.roles = %v, want [admin acme-owner]", roles)
	}
}
