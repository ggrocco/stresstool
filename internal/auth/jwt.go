package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"strings"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// Default JWT header block. User-provided Header values are merged on top.
var defaultJWTHeader = map[string]any{
	"alg": "HS256",
	"typ": "JWT",
}

// defaultJWTTTLSeconds is used when JWTAuthConfig.TTLSeconds is unset (0).
const defaultJWTTTLSeconds = 3600

// buildJWT assembles a signed JWT from the user's config block merged on top
// of the default header and payload blocks. String values inside either map
// are evaluated as placeholders so dynamic claims (e.g. {{ uuid() }}) work.
func buildJWT(cfg *config.JWTAuthConfig, eval *placeholders.Evaluator) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("jwt: config is nil")
	}
	if cfg.Signature == nil {
		return "", fmt.Errorf("jwt: signature is required")
	}

	ttl := cfg.TTLSeconds
	if ttl <= 0 {
		ttl = defaultJWTTTLSeconds
	}
	now := time.Now().Unix()

	// Payload defaults: iat and exp. Users can override either one.
	defaultPayload := map[string]any{
		"iat": now,
		"exp": now + int64(ttl),
	}

	header, err := mergeAndEvaluate(defaultJWTHeader, cfg.Header, eval)
	if err != nil {
		return "", fmt.Errorf("jwt: header: %w", err)
	}
	payload, err := mergeAndEvaluate(defaultPayload, cfg.Payload, eval)
	if err != nil {
		return "", fmt.Errorf("jwt: payload: %w", err)
	}

	alg, _ := header["alg"].(string)
	alg = strings.ToUpper(alg)
	if alg == "" {
		alg = "HS256"
		header["alg"] = alg
	}

	secret, err := eval.Evaluate(cfg.Signature.Secret)
	if err != nil {
		return "", fmt.Errorf("jwt: signature.secret: %w", err)
	}
	if secret == "" {
		return "", fmt.Errorf("jwt: signature.secret is required")
	}

	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("jwt: marshal header: %w", err)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("jwt: marshal payload: %w", err)
	}

	signingInput := base64URLEncode(headerBytes) + "." + base64URLEncode(payloadBytes)

	sig, err := signJWT(alg, []byte(secret), []byte(signingInput))
	if err != nil {
		return "", err
	}

	return signingInput + "." + base64URLEncode(sig), nil
}

// signJWT computes the HMAC signature for the given algorithm.
// Supported algorithms: HS256, HS384, HS512.
func signJWT(alg string, secret, signingInput []byte) ([]byte, error) {
	var h hash.Hash
	switch alg {
	case "HS256":
		h = hmac.New(sha256.New, secret)
	case "HS384":
		h = hmac.New(sha512.New384, secret)
	case "HS512":
		h = hmac.New(sha512.New, secret)
	default:
		return nil, fmt.Errorf("jwt: unsupported alg %q (supported: HS256, HS384, HS512)", alg)
	}
	h.Write(signingInput)
	return h.Sum(nil), nil
}

// base64URLEncode is standard JWT base64url-without-padding encoding.
func base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// mergeAndEvaluate returns a new map that is the shallow merge of `overrides`
// on top of `defaults`, with every string leaf evaluated via the placeholder
// engine. Nested maps and slices are traversed recursively so placeholders
// work anywhere in the structure.
func mergeAndEvaluate(defaults, overrides map[string]any, eval *placeholders.Evaluator) (map[string]any, error) {
	merged := make(map[string]any, len(defaults)+len(overrides))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return evaluateMap(merged, eval)
}

func evaluateMap(m map[string]any, eval *placeholders.Evaluator) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		ev, err := evaluateValue(v, eval)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = ev
	}
	return out, nil
}

func evaluateValue(v any, eval *placeholders.Evaluator) (any, error) {
	switch x := v.(type) {
	case string:
		return eval.Evaluate(x)
	case map[string]any:
		return evaluateMap(x, eval)
	case map[any]any:
		// yaml.v3 typically produces map[string]any, but older decodings may
		// yield map[any]any. Normalize to map[string]any.
		norm := make(map[string]any, len(x))
		for k, val := range x {
			ks, ok := k.(string)
			if !ok {
				ks = fmt.Sprintf("%v", k)
			}
			norm[ks] = val
		}
		return evaluateMap(norm, eval)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			ev, err := evaluateValue(item, eval)
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	default:
		return v, nil
	}
}
