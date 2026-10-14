package install

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every external installer dependency is either a fixture or a basic filesystem
// tool in a private PATH. No command can download a release or touch real hooks.
type shellEnv struct {
	root, tools, bin, downloads, script string
	env                                 []string
}

const fakeBinary = `#!/bin/sh
case "$1" in
version) echo 'agent-sessions test' ;;
uninstall)
 printf '%s\n' uninstall >> "$TEST_ROOT/binary.log"
 [ "${FAIL_UNINSTALL:-0}" = 0 ] || exit 1
 rm -f "$TEST_ROOT/managed-hook"
 ;;
*) exit 2 ;;
esac
`

func shellFixture(t *testing.T) *shellEnv {
	t.Helper()
	root := t.TempDir()
	e := &shellEnv{root: root, tools: filepath.Join(root, "tools"), bin: filepath.Join(root, "bin with spaces"), downloads: filepath.Join(root, "downloads")}
	var err error
	e.script, err = filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{e.tools, e.bin, e.downloads} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cat", "mktemp", "rm", "mkdir", "grep", "cp", "chmod", "mv"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(e.tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Use the real checksum tool against fake local assets, never fake verification.
	if path, err := exec.LookPath("sha256sum"); err == nil {
		if err := os.Symlink(path, filepath.Join(e.tools, "sha256sum")); err != nil {
			t.Fatal(err)
		}
	} else {
		path, err := exec.LookPath("shasum")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(e.tools, "shasum")); err != nil {
			t.Fatal(err)
		}
	}
	e.write(t, filepath.Join(e.downloads, "agent-sessions_linux_amd64"), fakeBinary)
	sha := sha256.Sum256([]byte(fakeBinary))
	e.write(t, filepath.Join(e.downloads, "checksums.txt"), fmt.Sprintf("%x  agent-sessions_linux_amd64\n", sha))
	e.tool(t, "uname", `case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; *) exit 1 ;; esac`)
	e.tool(t, "gh", `printf 'gh %s\n' "$*" >> "$TEST_ROOT/network.log"
case "$1 $2" in
'auth status') [ "${USE_GH:-0}" = 1 ] ;;
'release view') echo v1.2.3 ;;
'release download')
 [ "$3" = v1.2.3 ] || exit 1
 while [ "$#" -gt 0 ]; do
  if [ "$1" = --dir ]; then shift; dest=$1; fi
  shift
 done
 cp "$TEST_ROOT/downloads/agent-sessions_linux_amd64" "$dest/agent-sessions_linux_amd64"
 cp "$TEST_ROOT/downloads/checksums.txt" "$dest/checksums.txt"
 ;;
*) exit 1 ;;
esac`)
	e.tool(t, "curl", `printf 'curl %s\n' "$*" >> "$TEST_ROOT/network.log"
[ "$2" = -o ] || exit 1
out=$3
if [ "$out" = /dev/null ]; then
 printf '%s' https://github.com/mich-murphy/memory/releases/tag/v1.2.3
 exit 0
fi
case "$4" in
https://github.com/mich-murphy/memory/releases/download/v1.2.3/agent-sessions_linux_amd64) cp "$TEST_ROOT/downloads/agent-sessions_linux_amd64" "$out" ;;
https://github.com/mich-murphy/memory/releases/download/v1.2.3/checksums.txt) cp "$TEST_ROOT/downloads/checksums.txt" "$out" ;;
*) exit 1 ;;
esac`)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch name {
		case "HOME", "PATH", "TMPDIR", "BIN_DIR", "VERSION", "TEST_ROOT", "USE_GH", "FAIL_UNINSTALL":
		default:
			e.env = append(e.env, variable)
		}
	}
	e.env = append(e.env, "HOME="+root, "PATH="+e.tools, "TMPDIR="+root, "BIN_DIR="+e.bin, "TEST_ROOT="+root)
	return e
}

func (e *shellEnv) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func (e *shellEnv) tool(t *testing.T, name, script string) {
	t.Helper()
	path := filepath.Join(e.tools, name)
	// Replace the symlink rather than overwriting its real tool target.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	e.write(t, path, "#!/bin/sh\nset -eu\n"+script+"\n")
}

func (e *shellEnv) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/bin/sh", append([]string{e.script}, args...)...)
	cmd.Env = e.env
	return cmd
}

func (e *shellEnv) run(t *testing.T, success bool, args ...string) {
	t.Helper()
	out, err := e.command(t, args...).CombinedOutput()
	if (err == nil) != success {
		t.Fatalf("installer %v = %v, want success=%v:\n%s", args, err, success, out)
	}
}

func (e *shellEnv) noTemporaryFiles(t *testing.T) {
	t.Helper()
	for _, dir := range []string{e.root, e.bin} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "tmp.") || (strings.HasPrefix(entry.Name(), ".agent-sessions.") && entry.Name() != ".agent-sessions.new") {
				t.Errorf("installer left temporary file %s", filepath.Join(dir, entry.Name()))
			}
		}
	}
}

func TestShellInstallUsesOneReleaseAndIgnoresPredictableSymlink(t *testing.T) {
	for _, method := range []string{"curl", "gh"} {
		t.Run(method, func(t *testing.T) {
			e := shellFixture(t)
			if method == "gh" {
				e.env = append(e.env, "USE_GH=1")
			}
			victim := filepath.Join(e.root, "unrelated")
			e.write(t, victim, "keep me")
			if err := os.Symlink(victim, filepath.Join(e.bin, ".agent-sessions.new")); err != nil {
				t.Fatal(err)
			}
			e.run(t, true)
			if got, err := os.ReadFile(victim); err != nil || string(got) != "keep me" {
				t.Fatalf("unrelated file = %q, %v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(e.bin, "agent-sessions")); err != nil || string(got) != fakeBinary {
				t.Fatalf("installed binary = %q, %v", got, err)
			}
			e.noTemporaryFiles(t)
		})
	}
}

func TestShellConcurrentInstallsUseIndependentStagingFiles(t *testing.T) {
	e := shellFixture(t)
	const count = 8
	cmds := make([]*exec.Cmd, count)
	for i := range cmds {
		cmds[i] = e.command(t)
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("concurrent install = %v", err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(e.bin, "agent-sessions")); err != nil || string(got) != fakeBinary {
		t.Fatalf("installed binary = %q, %v", got, err)
	}
	e.noTemporaryFiles(t)
}

func TestShellInstallFailuresCleanUpAndPreserveInstalledBinary(t *testing.T) {
	for _, failure := range []string{"checksum", "copy", "signal", "chmod", "rename"} {
		t.Run(failure, func(t *testing.T) {
			e := shellFixture(t)
			binary := filepath.Join(e.bin, "agent-sessions")
			e.write(t, binary, "old binary")
			switch failure {
			case "checksum":
				e.write(t, filepath.Join(e.downloads, "checksums.txt"), strings.Repeat("0", 64)+"  agent-sessions_linux_amd64\n")
			case "copy", "signal":
				cp, err := exec.LookPath("cp")
				if err != nil {
					t.Fatal(err)
				}
				body := "exit 1"
				if failure == "signal" {
					body = `kill -TERM "$PPID"; exit 1`
				}
				e.tool(t, "cp", `case "$2" in */.agent-sessions.*) `+body+` ;; *) exec "`+cp+`" "$@" ;; esac`)
			case "chmod":
				e.tool(t, "chmod", "exit 1")
			case "rename":
				e.tool(t, "mv", "exit 1")
			}
			e.run(t, false)
			if got, err := os.ReadFile(binary); err != nil || string(got) != "old binary" {
				t.Fatalf("old binary = %q, %v", got, err)
			}
			e.noTemporaryFiles(t)
		})
	}
}

func TestShellUninstallIsOfflineAndPreservesData(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure%v", fail), func(t *testing.T) {
			e := shellFixture(t)
			binary := filepath.Join(e.bin, "agent-sessions")
			e.write(t, binary, fakeBinary)
			e.write(t, filepath.Join(e.root, "managed-hook"), "managed")
			for _, name := range []string{"config", "machine-id", "state.sqlite", "remote-data", "unrelated"} {
				e.write(t, filepath.Join(e.root, name), "preserve "+name)
			}
			for _, name := range []string{"curl", "gh", "uname", "mkdir", "mktemp"} {
				e.tool(t, name, `echo unexpected >> "$TEST_ROOT/unexpected"; exit 1`)
			}
			// Uninstall must not even validate the platform or release version.
			e.env = append(e.env, "VERSION=bad/version")
			if fail {
				e.env = append(e.env, "FAIL_UNINSTALL=1")
			}
			e.run(t, !fail, "uninstall")
			if _, err := os.Stat(binary); os.IsNotExist(err) == fail {
				t.Errorf("binary after uninstall = %v, want preserved=%v", err, fail)
			}
			if _, err := os.Stat(filepath.Join(e.root, "managed-hook")); os.IsNotExist(err) == fail {
				t.Errorf("hook after uninstall = %v, want preserved=%v", err, fail)
			}
			if _, err := os.Stat(filepath.Join(e.root, "unexpected")); !os.IsNotExist(err) {
				t.Errorf("uninstall ran a network/platform/setup command: %v", err)
			}
			for _, name := range []string{"config", "machine-id", "state.sqlite", "remote-data", "unrelated"} {
				if got, err := os.ReadFile(filepath.Join(e.root, name)); err != nil || string(got) != "preserve "+name {
					t.Errorf("preserved %s = %q, %v", name, got, err)
				}
			}
			if !fail {
				e.run(t, true, "uninstall")
			}
		})
	}
}

func TestShellInstallerValidatesArgumentsAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"unexpected"}, {"install", "uninstall"}} {
		e := shellFixture(t)
		e.run(t, len(args) == 1 && args[0] == "--help", args...)
		if _, err := os.Stat(filepath.Join(e.root, "network.log")); !os.IsNotExist(err) {
			t.Errorf("argument validation used network: %v", err)
		}
	}
	for _, version := range []string{"--repo=foreign", "v1.2.3 with spaces", "v1/../../other", "$(touch victim)", "v1.2.3;false"} {
		e := shellFixture(t)
		e.env = append(e.env, "VERSION="+version)
		e.run(t, false)
		if _, err := os.Stat(filepath.Join(e.root, "network.log")); !os.IsNotExist(err) {
			t.Errorf("invalid VERSION used network: %v", err)
		}
	}
}
