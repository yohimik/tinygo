//go:build !darwin

package main

import "crypto/tls"

// Everywhere but darwin the system roots load from files crypto/x509 already
// knows about, so the zero config (plus the name being dialled) is enough.
func probeTLSConfig(serverName string) *tls.Config {
	return &tls.Config{ServerName: serverName}
}
