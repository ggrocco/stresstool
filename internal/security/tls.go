package security

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSConfig holds TLS configuration for secure communication
type TLSConfig struct {
	// Enabled indicates if TLS should be used
	Enabled bool
	// CertFile is the path to the certificate file (for controller)
	CertFile string
	// KeyFile is the path to the key file (for controller)
	KeyFile string
	// CAFile is the path to the CA certificate file (for mutual TLS)
	CAFile string
	// ClientCertFile is the path to the client certificate (for nodes)
	ClientCertFile string
	// ClientKeyFile is the path to the client key (for nodes)
	ClientKeyFile string
	// ServerName is the expected server name for certificate validation (for nodes)
	ServerName string
}

// LoadServerTLSConfig creates a TLS config for the controller (server)
func LoadServerTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, fmt.Errorf("TLS enabled but cert or key file not specified")
	}

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13, // Require TLS 1.3
	}

	// If CA is specified, enable mutual TLS (client certificate verification)
	if cfg.CAFile != "" {
		caData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.ClientCAs = caPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig, nil
}

// LoadClientTLSConfig creates a TLS config for nodes (clients)
func LoadClientTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13, // Require TLS 1.3
	}

	// Set ServerName for certificate verification
	if cfg.ServerName != "" {
		tlsConfig.ServerName = cfg.ServerName
	}

	// If CA is specified, use it to verify server certificate
	if cfg.CAFile != "" {
		caData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.RootCAs = caPool
	}

	// If client cert is specified, load it for mutual TLS
	if cfg.ClientCertFile != "" && cfg.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
