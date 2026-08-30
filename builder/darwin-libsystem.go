package builder

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tinygo-org/tinygo/compileopts"
	"github.com/tinygo-org/tinygo/goenv"
)

// Symbols that libSystem exports but the minimal macOS SDK in
// lib/macos-minimal-sdk does not declare: its stub generator reads a fixed list
// of headers, and neither <sys/socket.h> nor <spawn.h> is on it, so these names
// are absent from the generated libSystem.s. Declare them here as the same kind
// of empty stub the generated file uses — the linker only needs to know the
// names live in libSystem.B.dylib.
var darwinExtraLibSystemSymbols = []string{
	// The BSD socket API, reached by the host netdev in src/net through the
	// standard library's syscall package.
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

	// The posix_spawn family, which src/os uses to start processes.
	// posix_spawn_file_actions_addchdir_np was added in macOS 10.15, so a
	// binary built with this toolchain needs at least that release even though
	// the deployment target is lower.
	"posix_spawn",
	"posix_spawn_file_actions_addchdir_np",
	"posix_spawn_file_actions_addclose",
	"posix_spawn_file_actions_adddup2",
	"posix_spawn_file_actions_destroy",
	"posix_spawn_file_actions_init",
	"posix_spawnattr_destroy",
	"posix_spawnattr_init",
	"posix_spawnattr_setflags",
	"posix_spawnattr_setpgroup",
	"posix_spawnattr_setsigmask",
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
