package builder

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tinygo-org/tinygo/compileopts"
	"github.com/tinygo-org/tinygo/goenv"
)

// The BSD socket API, which libSystem exports but the minimal macOS SDK in
// lib/macos-minimal-sdk does not declare: its stub generator reads a fixed list
// of headers that does not include <sys/socket.h>, so the symbols are absent
// from the generated libSystem.s. The host netdev in src/net reaches them
// through the standard library's syscall package, so declare them here as the
// same kind of empty stub the generated file uses — the linker only needs to
// know the names live in libSystem.B.dylib.
var darwinExtraLibSystemSymbols = []string{
	"accept",
	"bind",
	"connect",
	"getpeername",
	"getsockname",
	"getsockopt",
	"listen",
	"recvfrom",
	"recvmsg",
	"sendmsg",
	"sendto",
	"setsockopt",
	"shutdown",
	"socket",
	"socketpair",
}

// Create a job that builds a Darwin libSystem.dylib stub library. This library
// contains all the symbols needed so that we can link against it, but it
// doesn't contain any real symbol implementations.
func makeDarwinLibSystemJob(config *compileopts.Config, tmpdir string) *compileJob {
	return &compileJob{
		description: "compile Darwin libSystem.dylib",
		run: func(job *compileJob) (err error) {
			arch, _, _ := strings.Cut(config.Triple(), "-")
			job.result = filepath.Join(tmpdir, "libSystem.dylib")
			objpath := filepath.Join(tmpdir, "libSystem.o")
			inpath := filepath.Join(goenv.Get("TINYGOROOT"), "lib/macos-minimal-sdk/src", arch, "libSystem.s")

			// Compile assembly file to object file.
			flags := []string{
				"-nostdlib",
				"--target=" + config.Triple(),
				"-c",
				"-o", objpath,
				inpath,
			}
			if config.Options.PrintCommands != nil {
				config.Options.PrintCommands("clang", flags...)
			}
			err = runCCompiler(flags...)
			if err != nil {
				return err
			}

			// Compile the extra stubs the minimal SDK does not declare into a
			// second object file, so the generated one stays untouched.
			extrapath := filepath.Join(tmpdir, "libSystem-extra.s")
			extraobjpath := filepath.Join(tmpdir, "libSystem-extra.o")
			var extra strings.Builder
			extra.WriteString("// Stubs for symbols exported by libSystem but not declared in lib/macos-minimal-sdk.\n")
			for _, symbol := range darwinExtraLibSystemSymbols {
				extra.WriteString("\n.global _" + symbol + "\n_" + symbol + ":\n")
			}
			if err := os.WriteFile(extrapath, []byte(extra.String()), 0o666); err != nil {
				return err
			}
			flags = []string{
				"-nostdlib",
				"--target=" + config.Triple(),
				"-c",
				"-o", extraobjpath,
				extrapath,
			}
			if config.Options.PrintCommands != nil {
				config.Options.PrintCommands("clang", flags...)
			}
			err = runCCompiler(flags...)
			if err != nil {
				return err
			}

			// Link object files to dynamic library.
			platformVersion := strings.TrimPrefix(strings.Split(config.Triple(), "-")[2], "macosx")
			flags = []string{
				"-flavor", "darwin",
				"-demangle",
				"-dynamic",
				"-dylib",
				"-arch", arch,
				"-platform_version", "macos", platformVersion, platformVersion,
				"-install_name", "/usr/lib/libSystem.B.dylib",
				"-o", job.result,
				objpath,
				extraobjpath,
			}
			if config.Options.PrintCommands != nil {
				config.Options.PrintCommands("ld.lld", flags...)
			}
			return link("ld.lld", flags...)
		},
	}
}
