# sigprobe

A hand-run program that asks the signal layer of a hosted TinyGo binary for one
thing at a time — deliver a notified signal, deliver a second one, stop
delivering, restore the default action, ignore a signal, terminate on an
unregistered one — so a failure names the piece that broke rather than the
program that reached it.

Every check runs the probe binary again as a child, waits for the child to
print `READY`, sends it a signal, and reads what the child made of it. That is
the same shape a supervisor uses on a TinyGo-built CLI: the parent signals a
child by pid and waits for it to shut down.

It is not part of any test suite. It exists to be run against a *released*
toolchain, on a host of the target platform, when changing the `os/signal`
plumbing in `src/runtime` — in particular the two scheduler-specific halves in
`signal_cooperative.go` and `signal_threads.go`.

```
tinygo build -o /tmp/sigprobe ./tests/sigprobe
/tmp/sigprobe                 # every check
/tmp/sigprobe notify stop     # just these
```

Checks:

| name            | what it asks                                                      |
| --------------- | ----------------------------------------------------------------- |
| `notify`        | `signal.Notify` hands SIGINT to the channel                       |
| `notify-term`   | and SIGTERM too                                                   |
| `notifycontext` | `signal.NotifyContext` cancels its context                        |
| `repeat`        | two signals in a row both arrive                                  |
| `stop`          | `signal.Stop` returns, stops delivery, and restores the default   |
| `reset`         | `signal.Reset` restores the default action                        |
| `ignore`        | `signal.Ignore` keeps the process alive with no reader            |
| `unregistered`  | a signal nobody notified still terminates the process             |
| `busy`          | delivery lands while every thread is churning locks and goroutines|
| `gc`            | delivery lands under allocation pressure                          |
| `graceful`      | the child shuts down and exits 0, and the parent's wait returns   |

Every check should report `ok` on hosted linux and macOS, with the default
threads scheduler and with `-scheduler=tasks`. It also passes when built with
the ordinary Go toolchain, which is what makes it usable as an oracle: run
`go build ./tests/sigprobe` first if a result here looks surprising.

On a target without signals or a process model the program is not expected to
build usefully at all.
