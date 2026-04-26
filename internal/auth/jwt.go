package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// Default JWT header block. User-provided Header values are merged on top.
var defaultJWTHeader = map[string]string{
	"alg": "HS256",
	"typ": "JWT",
}

// defaultJWTTTLSeconds is used when JWTAuthConfig.TTLSeconds is unset (0).
const defaultJWTTTLSeconds = 3600

// buildJWT assembles a signed JWT from the user's config block merged on top
// of the default header and payload blocks. Every value is evaluated through
// the placeholder engine then coerced to JSON types for the final token.
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
	defaultPayload := map[string]string{
		"iat": strconv.FormatInt(now, 10),
		"exp": strconv.FormatInt(now+int64(ttl), 10),
	}

	header, err := mergeAndEvaluate(defaultJWTHeader, cfg.Header, eval)
	if err != nil {
		return "", fmt.Errorf("jwt: header: %w", err)
	}
	payload, err := mergeAndEvaluate(defaultPayload, cfg.Payload, eval)
	if err != nil {
		return "", fmt.Errorf("jwt: payload: %w", err)
	}

	alg := strings.ToUpper(header["alg"])
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

	headerBytes, err := claimsJSON(header)
	if err != nil {
		return "", fmt.Errorf("jwt: marshal header: %w", err)
	}
	payloadBytes, err := claimsJSON(payload)
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

// claimsJSON serialises a flat string map into a JSON object, coercing values
// that look like numbers or booleans into their native JSON types so the token
// conforms to RFC 7519 (e.g. "exp" must be a Number, not a String).
func claimsJSON(m map[string]string) ([]byte, error) {
	obj := make(map[string]any, len(m))
	for k, v := range m {
		obj[k] = coerceJSONValue(v)
	}
	return json.Marshal(obj)
}

// coerceJSONValue converts a string to a native JSON type when possible.
// Priority: integer → float → boolean → string.
func coerceJSONValue(s string) any {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
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
// on top of `defaults`, with every value evaluated via the placeholder engine.
func mergeAndEvaluate(defaults, overrides map[string]string, eval *placeholders.Evaluator) (map[string]string, error) {
	merged := make(map[string]string, len(defaults)+len(overrides))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	for k, v := range merged {
		ev, err := eval.Evaluate(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		merged[k] = ev
	}
	return merged, nil
}
