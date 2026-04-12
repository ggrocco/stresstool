package protocol

import (
	"testing"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/runner"
)

func TestConfigRoundTrip(t *testing.T) {
	f := false
	cfg := &config.Config{
		Funcs: []config.FuncDef{{Name: "x", Cmd: []string{"echo", "hi"}}},
		Auth: &config.AuthConfig{
			BasicAuth: &config.BasicAuthConfig{
				Username: "admin",
				Password: "secret",
			},
		},
		Tests: []config.Test{{
			Name: "t1", Path: "/p", Method: "GET",
			RequestsPerSecond: 10, Threads: 2, RunSeconds: 5,
			Headers: map[string]string{"H": "v"},
			Body:    "{}",
			Assert:  &config.Assertion{StatusCode: 200, MaxLatencyMs: 100},
			Nodes:   map[string]config.Node{"n1": {RequestsPerSecond: 5, Threads: 1}},
		}, {
			Name: "t2", Path: "/q", Method: "POST",
			RequestsPerSecond: 5, Threads: 1, RunSeconds: 3,
			Auth: &f,
		}},
	}
	pb := ConfigToProto(cfg)
	out, err := ConfigFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Funcs) != 1 || out.Funcs[0].Name != "x" {
		t.Fatalf("funcs: %+v", out.Funcs)
	}
	if out.Auth == nil || out.Auth.BasicAuth == nil {
		t.Fatal("auth round-trip: missing basic_auth")
	}
	if out.Auth.BasicAuth.Username != "admin" || out.Auth.BasicAuth.Password != "secret" {
		t.Fatalf("auth round-trip: %+v", out.Auth.BasicAuth)
	}
	if len(out.Tests) != 2 || out.Tests[0].Name != "t1" || out.Tests[0].RequestsPerSecond != 10 {
		t.Fatalf("tests: %+v", out.Tests)
	}
	if out.Tests[0].Assert == nil || out.Tests[0].Assert.StatusCode != 200 {
		t.Fatal("assert round-trip")
	}
	if len(out.Tests[0].Nodes) != 1 {
		t.Fatal("nodes round-trip")
	}
	// t1 has no auth override → Auth should be nil
	if out.Tests[0].Auth != nil {
		t.Fatal("t1.Auth should be nil")
	}
	// t2 has auth:false → AuthDisabled round-trip
	if out.Tests[1].Auth == nil || *out.Tests[1].Auth != false {
		t.Fatal("t2 auth_disabled round-trip")
	}
}

func TestConfigRoundTrip_JWT(t *testing.T) {
	cfg := &config.Config{
		Auth: &config.AuthConfig{
			JWT: &config.JWTAuthConfig{
				Header: map[string]string{
					"alg": "HS384",
					"typ": "JWT",
					"kid": "k1",
				},
				Payload: map[string]string{
					"iss": "stresstool",
					"sub": "loadtest",
					"exp": "9999999999",
				},
				Signature:  &config.JWTSignatureConfig{Secret: "shh"},
				TTLSeconds: 900,
			},
		},
		Tests: []config.Test{{
			Name: "t", Path: "/p", Method: "GET",
			RequestsPerSecond: 1, Threads: 1, RunSeconds: 1,
		}},
	}
	pb := ConfigToProto(cfg)
	out, err := ConfigFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	if out.Auth == nil || out.Auth.JWT == nil {
		t.Fatal("jwt round-trip: missing")
	}
	j := out.Auth.JWT
	if j.Header["alg"] != "HS384" || j.Header["kid"] != "k1" {
		t.Errorf("header round-trip: %+v", j.Header)
	}
	if j.Payload["iss"] != "stresstool" || j.Payload["sub"] != "loadtest" {
		t.Errorf("payload round-trip: %+v", j.Payload)
	}
	if j.Signature == nil || j.Signature.Secret != "shh" {
		t.Errorf("signature round-trip: %+v", j.Signature)
	}
	if j.TTLSeconds != 900 {
		t.Errorf("ttl_seconds = %d, want 900", j.TTLSeconds)
	}
}

func TestTestResultRoundTrip(t *testing.T) {
	ct := &config.Test{Name: "t", Path: "/x", Method: "GET", RequestsPerSecond: 1, Threads: 1, RunSeconds: 1}
	m := runner.NewMetricsSnapshot(3, 2, 1, 0,
		[]time.Duration{time.Millisecond, 2 * time.Millisecond},
		map[int]int64{200: 2, 500: 1},
		map[string]int64{"err": 1},
	)
	res := &runner.TestResult{
		Test:         ct,
		Metrics:      m,
		Passed:       false,
		Errors:       []string{"e1"},
		StoppedEarly: true,
	}
	pb := TestResultToProto(res)
	out, err := TestResultFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test.Name != "t" || !out.StoppedEarly || len(out.Errors) != 1 {
		t.Fatalf("result: %+v", out)
	}
	if out.Metrics.TotalRequests != 3 || len(out.Metrics.Latencies) != 2 {
		t.Fatalf("metrics: %+v", out.Metrics)
	}
}
