// netprobe asks the four network layers a hosted program needs — TCP, TLS,
// plain HTTP and HTTPS — one at a time, so a failure names the layer it
// happened at rather than the command that reached it.
//
// Run it as: netprobe {ldflags|tcp|tls|http|https|dns}
package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// The two declarations -X can be pointed at. TinyGo applies the flag to the
// bare one and silently ignores it for the one with an initialiser; gc stamps
// both.
var (
	Bare      string
	Initialed = "dev"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: netprobe {ldflags|tcp|tls|http|https|dns}")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ldflags":
		fmt.Printf("bare=%q initialed=%q\n", Bare, Initialed)
	case "tcp":
		c, err := net.DialTimeout("tcp", "api.github.com:443", 15*time.Second)
		fmt.Println("tcp err:", err)
		if c != nil {
			c.Close()
		}
	case "tls":
		// probeTLSConfig supplies explicit RootCAs on darwin, where the
		// platform verifier is a stub; it is nil-but-for-ServerName elsewhere.
		c, err := tls.Dial("tcp", "api.github.com:443", probeTLSConfig("api.github.com"))
		fmt.Println("tls err:", err)
		if c != nil {
			fmt.Println("tls version:", c.ConnectionState().Version, "cipher:", c.ConnectionState().CipherSuite)
			c.Close()
		}
	case "http":
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Get("http://example.com")
		fmt.Println("plain http err:", err)
		if resp != nil {
			fmt.Println("plain http status:", resp.Status)
			resp.Body.Close()
		}
	case "https":
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Get("https://api.github.com/")
		fmt.Println("https err:", err)
		if resp != nil {
			fmt.Println("https status:", resp.Status)
			resp.Body.Close()
		}
	case "dns":
		for _, host := range []string{"localhost", "api.github.com", "no-such-host.invalid"} {
			addrs, err := net.LookupHost(host)
			fmt.Printf("dns %s: addrs=%v err=%v", host, addrs, err)
			if dnsErr, ok := err.(*net.DNSError); ok {
				fmt.Printf(" isNotFound=%v isTimeout=%v", dnsErr.IsNotFound, dnsErr.IsTimeout)
			}
			fmt.Println()
		}
	default:
		fmt.Println("unknown probe:", os.Args[1])
		os.Exit(2)
	}
}
