package protocol

import (
	"fmt"
	"math"
	"time"

	"stresstool/internal/config"
	payloadpb "stresstool/internal/protocol/payloadpb/api/v1"
	"stresstool/internal/runner"
)

// safeInt32 clamps an int to the int32 range to avoid silent overflow.
func safeInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

// copyStringMap returns a shallow copy of a string map (nil-safe).
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ConfigToProto converts a config.Config to protobuf.
func ConfigToProto(c *config.Config) *payloadpb.Config {
	if c == nil {
		return nil
	}
	out := &payloadpb.Config{
		Funcs: make([]*payloadpb.FuncDef, 0, len(c.Funcs)),
		Tests: make([]*payloadpb.Test, 0, len(c.Tests)),
		Auth:  authConfigToProto(c.Auth),
	}
	for _, f := range c.Funcs {
		out.Funcs = append(out.Funcs, &payloadpb.FuncDef{
			Name: f.Name,
			Cmd:  append([]string(nil), f.Cmd...),
		})
	}
	for _, t := range c.Tests {
		out.Tests = append(out.Tests, testToProto(&t))
	}
	return out
}

func testToProto(t *config.Test) *payloadpb.Test {
	if t == nil {
		return nil
	}
	pb := &payloadpb.Test{
		Name:              t.Name,
		Path:              t.Path,
		Method:            t.Method,
		RequestsPerSecond: safeInt32(t.RequestsPerSecond),
		Threads:           safeInt32(t.Threads),
		RunSeconds:        safeInt32(t.RunSeconds),
		WarmupSeconds:     safeInt32(t.WarmupSeconds),
		Headers:           map[string]string{},
		Body:              t.Body,
		Nodes:             map[string]*payloadpb.NodeOverride{},
	}
	for k, v := range t.Headers {
		pb.Headers[k] = v
	}
	if t.Assert != nil {
		pb.Assert = &payloadpb.Assertion{
			StatusCode:    safeInt32(t.Assert.StatusCode),
			BodyContains:  t.Assert.BodyContains,
			BodyEquals:    t.Assert.BodyEquals,
			BodyNotEquals: t.Assert.BodyNotEquals,
			MaxLatencyMs:  safeInt32(t.Assert.MaxLatencyMs),
		}
	}
	if t.Auth != nil && !*t.Auth {
		pb.AuthDisabled = true
	}
	for name, n := range t.Nodes {
		pb.Nodes[name] = &payloadpb.NodeOverride{
			RequestsPerSecond: safeInt32(n.RequestsPerSecond),
			Threads:           safeInt32(n.Threads),
			WarmupSeconds:     safeInt32(n.WarmupSeconds),
		}
	}
	return pb
}

// ConfigFromProto builds config.Config from protobuf.
func ConfigFromProto(pb *payloadpb.Config) (*config.Config, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil config")
	}
	out := &config.Config{
		Funcs: make([]config.FuncDef, 0, len(pb.Funcs)),
		Auth:  authConfigFromProto(pb.Auth),
		Tests: make([]config.Test, 0, len(pb.Tests)),
	}
	for _, f := range pb.Funcs {
		if f == nil {
			continue
		}
		out.Funcs = append(out.Funcs, config.FuncDef{
			Name: f.Name,
			Cmd:  append([]string(nil), f.Cmd...),
		})
	}
	for _, t := range pb.Tests {
		if t == nil {
			continue
		}
		ct, err := testFromProto(t)
		if err != nil {
			return nil, err
		}
		out.Tests = append(out.Tests, *ct)
	}
	return out, nil
}

func testFromProto(t *payloadpb.Test) (*config.Test, error) {
	if t == nil {
		return nil, fmt.Errorf("nil test")
	}
	out := &config.Test{
		Name:              t.Name,
		Path:              t.Path,
		Method:            t.Method,
		RequestsPerSecond: int(t.RequestsPerSecond),
		Threads:           int(t.Threads),
		RunSeconds:        int(t.RunSeconds),
		WarmupSeconds:     int(t.WarmupSeconds),
		Headers:           map[string]string{},
		Body:              t.Body,
		Nodes:             map[string]config.Node{},
	}
	for k, v := range t.Headers {
		out.Headers[k] = v
	}
	if t.Assert != nil {
		out.Assert = &config.Assertion{
			StatusCode:    int(t.Assert.StatusCode),
			BodyContains:  t.Assert.BodyContains,
			BodyEquals:    t.Assert.BodyEquals,
			BodyNotEquals: t.Assert.BodyNotEquals,
			MaxLatencyMs:  int(t.Assert.MaxLatencyMs),
		}
	}
	if t.AuthDisabled {
		f := false
		out.Auth = &f
	}
	for name, n := range t.Nodes {
		if n == nil {
			continue
		}
		out.Nodes[name] = config.Node{
			RequestsPerSecond: int(n.RequestsPerSecond),
			Threads:           int(n.Threads),
			WarmupSeconds:     int(n.WarmupSeconds),
		}
	}
	return out, nil
}

func authConfigToProto(a *config.AuthConfig) *payloadpb.AuthConfig {
	if a == nil {
		return nil
	}
	pb := &payloadpb.AuthConfig{}
	if a.BasicAuth != nil {
		pb.BasicAuth = &payloadpb.BasicAuthConfig{
			Username: a.BasicAuth.Username,
			Password: a.BasicAuth.Password,
		}
	}
	if a.Bearer != nil {
		pb.Bearer = &payloadpb.BearerAuthConfig{
			Token: a.Bearer.Token,
		}
	}
	if a.APIKey != nil {
		pb.ApiKey = &payloadpb.APIKeyAuthConfig{
			Header: a.APIKey.Header,
			Key:    a.APIKey.Key,
		}
	}
	if a.OAuth2ClientCredentials != nil {
		pb.Oauth2ClientCredentials = &payloadpb.OAuth2ClientCredentialsConfig{
			TokenUrl:     a.OAuth2ClientCredentials.TokenURL,
			ClientId:     a.OAuth2ClientCredentials.ClientID,
			ClientSecret: a.OAuth2ClientCredentials.ClientSecret,
			Scopes:       append([]string(nil), a.OAuth2ClientCredentials.Scopes...),
		}
	}
	if a.JWT != nil {
		jwt := &payloadpb.JWTAuthConfig{
			Header:     copyStringMap(a.JWT.Header),
			Payload:    copyStringMap(a.JWT.Payload),
			TtlSeconds: safeInt32(a.JWT.TTLSeconds),
		}
		if a.JWT.Signature != nil {
			jwt.Signature = &payloadpb.JWTSignatureConfig{
				Secret: a.JWT.Signature.Secret,
			}
		}
		pb.Jwt = jwt
	}
	return pb
}

func authConfigFromProto(pb *payloadpb.AuthConfig) *config.AuthConfig {
	if pb == nil {
		return nil
	}
	a := &config.AuthConfig{}
	if pb.BasicAuth != nil {
		a.BasicAuth = &config.BasicAuthConfig{
			Username: pb.BasicAuth.Username,
			Password: pb.BasicAuth.Password,
		}
	}
	if pb.Bearer != nil {
		a.Bearer = &config.BearerAuthConfig{
			Token: pb.Bearer.Token,
		}
	}
	if pb.ApiKey != nil {
		a.APIKey = &config.APIKeyAuthConfig{
			Header: pb.ApiKey.Header,
			Key:    pb.ApiKey.Key,
		}
	}
	if pb.Oauth2ClientCredentials != nil {
		a.OAuth2ClientCredentials = &config.OAuth2ClientCredentialsConfig{
			TokenURL:     pb.Oauth2ClientCredentials.TokenUrl,
			ClientID:     pb.Oauth2ClientCredentials.ClientId,
			ClientSecret: pb.Oauth2ClientCredentials.ClientSecret,
			Scopes:       append([]string(nil), pb.Oauth2ClientCredentials.Scopes...),
		}
	}
	if pb.Jwt != nil {
		jwt := &config.JWTAuthConfig{
			Header:     copyStringMap(pb.Jwt.Header),
			Payload:    copyStringMap(pb.Jwt.Payload),
			TTLSeconds: int(pb.Jwt.TtlSeconds),
		}
		if pb.Jwt.Signature != nil {
			jwt.Signature = &config.JWTSignatureConfig{
				Secret: pb.Jwt.Signature.Secret,
			}
		}
		a.JWT = jwt
	}
	return a
}

// TestResultToProto converts runner.TestResult to protobuf TestResult.
func TestResultToProto(r *runner.TestResult) *payloadpb.TestResult {
	if r == nil {
		return nil
	}
	pb := &payloadpb.TestResult{
		Test:         testToProto(r.Test),
		Metrics:      metricsToProto(r.Metrics),
		Passed:       r.Passed,
		Errors:       append([]string(nil), r.Errors...),
		StoppedEarly: r.StoppedEarly,
	}
	if r.Assertions != nil {
		pb.Assertions = &payloadpb.Assertions{}
	}
	return pb
}

func metricsToProto(m *runner.Metrics) *payloadpb.Metrics {
	if m == nil {
		return nil
	}
	pb := &payloadpb.Metrics{
		TotalRequests:     m.TotalRequests,
		SuccessCount:      m.SuccessCount,
		FailureCount:      m.FailureCount,
		AssertionFailures: m.AssertionFailures,
		LatenciesNanos:    make([]int64, 0, len(m.Latencies)),
		StatusCodes:       map[int32]int64{},
		Errors:            map[string]int64{},
	}
	for _, d := range m.Latencies {
		pb.LatenciesNanos = append(pb.LatenciesNanos, d.Nanoseconds())
	}
	for code, n := range m.StatusCodes {
		pb.StatusCodes[safeInt32(code)] = n
	}
	for msg, n := range m.Errors {
		pb.Errors[msg] = n
	}
	return pb
}

// TestResultFromProto builds runner.TestResult from protobuf (Assertions is nil).
func TestResultFromProto(pb *payloadpb.TestResult) (*runner.TestResult, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil test result")
	}
	t, err := testFromProto(pb.Test)
	if err != nil {
		return nil, err
	}
	m, err := metricsFromProto(pb.Metrics)
	if err != nil {
		return nil, err
	}
	return &runner.TestResult{
		Test:         t,
		Metrics:      m,
		Assertions:   nil,
		Passed:       pb.Passed,
		Errors:       append([]string(nil), pb.Errors...),
		StoppedEarly: pb.StoppedEarly,
	}, nil
}

func metricsFromProto(pb *payloadpb.Metrics) (*runner.Metrics, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil metrics")
	}
	latencies := make([]time.Duration, 0, len(pb.LatenciesNanos))
	for _, ns := range pb.LatenciesNanos {
		latencies = append(latencies, durationFromNanos(ns))
	}
	statusCodes := make(map[int]int64, len(pb.StatusCodes))
	for code, n := range pb.StatusCodes {
		statusCodes[int(code)] = n
	}
	errors := make(map[string]int64, len(pb.Errors))
	for msg, n := range pb.Errors {
		errors[msg] = n
	}
	return runner.NewMetricsSnapshot(
		pb.TotalRequests,
		pb.SuccessCount,
		pb.FailureCount,
		pb.AssertionFailures,
		latencies,
		statusCodes,
		errors,
	), nil
}

func durationFromNanos(ns int64) time.Duration {
	// Protobuf latencies are int64 nanoseconds; guard overflow for very large values.
	if ns < 0 {
		return 0
	}
	return time.Duration(ns)
}
