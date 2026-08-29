//go:build darwin

package main

import (
	"crypto/tls"
	"crypto/x509"
	"os"
)

// darwin has no usable system trust store from TinyGo: crypto/x509's macOS
// verifier is a stub, and a nil RootCAs sends verification down that path. A
// non-nil pool makes x509 do pure-Go chain building instead, so the probe
// loads PEM roots from $SSL_CERT_FILE or the file macOS ships at
// /etc/ssl/cert.pem.
func probeTLSConfig(serverName string) *tls.Config {
	cfg := &tls.Config{ServerName: serverName}
	path := os.Getenv("SSL_CERT_FILE")
	if path == "" {
		path = "/etc/ssl/cert.pem"
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	pool := x509.NewCertPool()
	if pool.AppendCertsFromPEM(pem) {
		cfg.RootCAs = pool
	}
	return cfg
}
