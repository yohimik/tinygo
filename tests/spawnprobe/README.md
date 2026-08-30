# spawnprobe

A hand-run program that asks the process layer of a hosted TinyGo binary for
one thing at a time — start a program, collect its output, read its exit
status, feed it a stdin, give it a working directory, kill it — so a failure
names the piece that broke rather than the command that reached it.

It is not part of any test suite. The `os` package's own tests cover the same
ground on the machine that runs `make test`; this exists to be run against a
*released* toolchain, on a host of the target platform, when changing
`src/os`'s posix_spawn implementation or the darwin libSystem stubs in
`builder/`.

```
tinygo build -o /tmp/spawnprobe ./tests/spawnprobe
/tmp/spawnprobe               # every check
/tmp/spawnprobe dir stdin     # just these
```

Checks:

| name           | what it asks                                                     |
| -------------- | ---------------------------------------------------------------- |
| `output`       | `CombinedOutput` of `/bin/echo`                                  |
| `stderr`       | the child's stderr lands in the combined output                  |
| `exitstatus`   | a non-zero exit is an `*exec.ExitError` with a real `ProcessState`|
| `env`          | `cmd.Env` reaches the child                                      |
| `inheritedenv` | a nil `cmd.Env` inherits the parent's environment                |
| `dir`          | `cmd.Dir` becomes the child's working directory                  |
| `stdin`        | the child reads a pipe the parent writes                         |
| `extrafiles`   | `cmd.ExtraFiles[0]` becomes the child's descriptor 3             |
| `noleak`       | an unrelated pipe is not inherited, so it still reports EOF      |
| `kill`         | `exec.CommandContext` kills the child when the context expires   |
| `notexist`     | a missing binary is `os.ErrNotExist`                             |
| `concurrent`   | sixteen simultaneous spawns all return their own output          |

Cross-compiling works the same way (`GOOS=darwin GOARCH=arm64 tinygo build …`);
the resulting binary runs on a host of that platform. Every check should report
`ok` on hosted linux and macOS. On a target without a process model the program
is not expected to build usefully at all — `os.StartProcess` there still
returns `ErrNotImplemented`.
