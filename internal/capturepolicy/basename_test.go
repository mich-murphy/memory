package capturepolicy

import (
	"path/filepath"
	"strings"
	"testing"
)

// regexpBaseName is the pre-gate implementation. Keep backupSuffix as the oracle:
// candidate checks may admit extra names, but must never hide a regexp match.
func regexpBaseName(path string) string {
	name := strings.ToLower(filepath.Base(path))
	for {
		base := backupSuffix.ReplaceAllString(name, "")
		if base == name {
			return name
		}
		name = base
	}
}

func TestBaseNameMatchesRegexp(t *testing.T) {
	for _, tc := range []struct {
		path, want string
	}{
		{"", "."},
		{"/", "/"},
		{"/dir.before-upgrade/AUTH.JSON/", "auth.json"},
		{"auth.json.before-a.jsonl", "auth.json"},
		{"auth.json.before-a.sqlite", "auth.json"},
		{"auth.json.before-a.json", "auth.json"},
		{"auth.json.before-a.bak.before-b.old", "auth.json"},
		{"auth.json.agent-sessions-backup---", "auth.json"},
		{"auth.json.agent-sessions-backup-", "auth.json.agent-sessions-backup-"},
		{"auth.json.agent-sessions-backup-a", "auth.json.agent-sessions-backup-a"},
		{"auth.json.bz2", "auth.json"},
		{"auth.json.~123~.tar.gz-20261014-235959.old", "auth.json"},
		{"auth.json-20261014-12345", "auth.json-20261014-12345"},
		{"auth.json-20261014-2359590", "auth.json-20261014-2359590"},
		{"auth.json.before-_a", "auth.json.before-_a"},
		{"auth.json.before-a_", "auth.json"},
		{"auth.json.before-a.", "auth.json"},
		{"auth.json.before-a\n", "auth.json.before-a\n"},
		{"auth.json.before-a\x00", "auth.json.before-a\x00"},
		{"auth.json\x00.bak", "auth.json\x00"},
		{"AUTH.JSON.BEFORE-K", "auth.json"},
		{"AUTH.JSON.BEFORE-İ", "auth.json"},
		{"AUTH.JSON.BEFORE-É", "auth.json.before-é"},
		{"auth.json.bak\xff", "auth.json.bak�"},
		{"auth.json\xff.bak", "auth.json�"},
	} {
		if got := regexpBaseName(tc.path); got != tc.want {
			t.Fatalf("regexpBaseName(%q) = %q, want %q", tc.path, got, tc.want)
		}
		if got := BaseName(tc.path); got != tc.want {
			t.Errorf("BaseName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}

	// Cross families and both orders: .before- can consume earlier than the
	// rightmost suffix, while stripping another suffix can reveal it later.
	suffixes := []string{
		"", ".bak", ".backup", ".orig", ".old", "~", "~0~", ".~123~",
		"-20261014", "-20261014-235959", ".before-a", ".before-a_",
		".before-a.", ".before-a-", ".agent-sessions-backup-20261014-101010",
		".agent-sessions-backup---", ".gz", ".bz2", ".xz", ".zst", ".zstd",
		".zip", ".7z", ".rar", ".tar", ".tgz", ".jsonl", ".json", ".sqlite",
		".example", ".before-", ".before-_", ".before-é", "\x00", "\n", "\xff",
	}
	for _, first := range suffixes {
		for _, second := range suffixes {
			for _, prefix := range []string{"auth.json", "AUTH.JSON", "/dir.bak/KİÉ\x00\xff"} {
				path := prefix + first + second
				if got, want := BaseName(path), regexpBaseName(path); got != want {
					t.Fatalf("BaseName(%q) = %q, want regexp %q", path, got, want)
				}
			}
		}
	}
	// Exercise every possible final byte, including each .before- character
	// and invalid UTF-8. The gate operates on the already folded basename.
	for last := range 256 {
		for _, prefix := range []string{"auth.json", "auth.json.before-a", "auth.json.agent-sessions-backup-"} {
			path := prefix + string([]byte{byte(last)})
			if got, want := BaseName(path), regexpBaseName(path); got != want {
				t.Fatalf("BaseName(%q) = %q, want regexp %q", path, got, want)
			}
		}
	}
}

func TestBaseNameOrdinaryNamesDoNotAllocate(t *testing.T) {
	for _, path := range []string{
		"/home/agent/sessions/019a7c28-1234-5678-abcd-0123456789ab.jsonl",
		"/home/agent/projects/settings.json",
		"/home/agent/state_5.sqlite",
		"/home/agent/projects",
		"/home/agent/project/artifact.txt",
		"/home/agent/project/package-lock",
		"/home/agent/project/workshop",
		"/home/agent/project/catalog",
		"/home/agent/project/record",
		"/home/agent/project/blitz",
		"/home/agent/project/worker",
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var got string
			allocs := testing.AllocsPerRun(100, func() { got = BaseName(path) })
			if want := filepath.Base(path); got != want {
				t.Fatalf("BaseName(%q) = %q, want %q", path, got, want)
			}
			// Lowercase names need neither case-folding storage nor a regexp
			// result copy. The old ReplaceAllString path allocated three times.
			if allocs != 0 {
				t.Errorf("BaseName(%q) allocations = %g, want 0", path, allocs)
			}
		})
	}
}

func BenchmarkBaseName(b *testing.B) {
	for _, tc := range []struct {
		name, path string
	}{
		{"jsonl", "/home/agent/sessions/019a7c28-1234-5678-abcd-0123456789ab.jsonl"},
		{"json", "/home/agent/projects/settings.json"},
		{"sqlite", "/home/agent/state_5.sqlite"},
		{"ancestor", "/home/agent/projects"},
		{"editor", "auth.json.~123~"},
		{"dated", "auth.json-20261014-235959"},
		{"installer", "auth.json.agent-sessions-backup-20261014-101010"},
		{"before", "auth.json.before-update.sqlite"},
		{"mixed", "AUTH.JSON.bak.old.tar.bz2~"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				BaseName(tc.path)
			}
		})
	}
}

// The prefilter may admit extra names, but every regexp suffix must reach the
// unchanged oracle, including when stripping reveals another suffix.
func TestBackupSuffixPrefilterLiteralEndings(t *testing.T) {
	for _, name := range []string{"artifact.txt", "package-lock", "workshop", "catalog", "record", "blitz", "worker"} {
		if mayHaveBackupSuffix(name) {
			t.Errorf("ordinary name %q reached backup regexp", name)
		}
	}
	for _, suffix := range []string{".bak", ".backup", ".orig", ".old", ".gz", ".bz2", ".xz", ".zst", ".zstd", ".zip", ".7z", ".rar", ".tar", ".tgz", "~", ".~123~", "-20261014", "-20261014-235959", ".agent-sessions-backup---", ".before-artifact.txt", ".before-package-lock", ".before-workshop", ".before-catalog", ".before-record", ".before-blitz", ".before-worker"} {
		for _, tail := range []string{"", ".bak.old.tar.gz~"} {
			name := "auth.json" + suffix + tail
			if !mayHaveBackupSuffix(name) {
				t.Errorf("backup %q skipped backup regexp", name)
			}
			if got, want := BaseName(name), regexpBaseName(name); got != want {
				t.Errorf("BaseName(%q) = %q, want oracle %q", name, got, want)
			}
		}
	}
}
