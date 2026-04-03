package cli

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSOptions configures TLS for gRPC controller (server) and node (client).
type TLSOptions struct {
	CertFile string
	KeyFile  string
	CAFile   string
	Insecure bool
}

// ServerTLSConfig returns TLS config for the gRPC server. If opts.Insecure, returns nil, nil.
// If CAFile is set, client certificates are required (mTLS).
func ServerTLSConfig(opts TLSOptions) (*tls.Config, error) {
	if opts.Insecure {
		return nil, nil
	}
	if opts.CertFile == "" || opts.KeyFile == "" {
		return nil, fmt.Errorf("TLS enabled: --tls-cert and --tls-key are required (or use --insecure)")
	}
	cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if opts.CAFile != "" {
		caPEM, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("no CA certificates parsed from %s", opts.CAFile)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// ClientTLSConfig returns TLS config for the gRPC client. If opts.Insecure, returns nil, nil.
func ClientTLSConfig(opts TLSOptions) (*tls.Config, error) {
	if opts.Insecure {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if opts.CAFile != "" {
		caPEM, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("no CA certificates parsed from %s", opts.CAFile)
		}
		cfg.RootCAs = pool
	} else {
		return nil, fmt.Errorf("TLS enabled: --tls-ca is required to verify the controller (or use --insecure)")
	}
	if opts.CertFile != "" && opts.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
