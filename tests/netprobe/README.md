# netprobe

A hand-run program that asks the four network layers a hosted TinyGo binary
needs — TCP, TLS, plain HTTP and HTTPS — one at a time, plus a DNS layer, so a
failure names the layer it happened at rather than the command that reached it.

It is not part of any test suite: it talks to the public internet, which a CI
job must not depend on. Run it against a build of this tree when changing the
host netdev in `src/net`, the `crypto/tls` override in `loader/goroot.go`, or
the darwin libSystem stubs in `builder/`.

```
tinygo build -o /tmp/netprobe ./tests/netprobe
/tmp/netprobe tcp     # net.DialTimeout to api.github.com:443
/tmp/netprobe tls     # crypto/tls handshake, real certificate verification
/tmp/netprobe http    # net/http GET over plaintext
/tmp/netprobe https   # net/http GET over TLS
/tmp/netprobe dns     # /etc/hosts, a real name, and an NXDOMAIN
/tmp/netprobe ldflags # which of the two -X targets the linker stamped
```

Cross-compiling works the same way (`GOOS=darwin GOARCH=arm64 tinygo build …`);
the resulting binary runs on a host of that platform.

Expected results on a hosted linux or macOS target: every layer reports a nil
error, `https` reports `200 OK`, and the `dns` layer resolves `/etc/hosts`
entries, resolves a real name, and reports `isNotFound=true` for a name that
does not exist.

The `tls` layer supplies explicit `RootCAs` on darwin. That is a real
limitation rather than a convenience: TinyGo's `crypto/x509/internal/macos` is a
stub, so a `tls.Dial` with a nil config cannot verify anything on macOS. The
`https` layer needs no such help because `net/http` supplies the same roots for
itself.
