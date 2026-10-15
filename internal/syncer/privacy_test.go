package syncer

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestRawGuardWithholdsAliasesAndDoesNotScanTranscripts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	body := []byte("export API_KEY=fixture-private-value\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.sh")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, alias} {
		for _, role := range []adapters.Role{adapters.RoleUnknown, adapters.RoleConfig, adapters.RoleMain, adapters.RoleSubagent, adapters.RoleSuperseded} {
			sf := adapters.SessionFile{Agent: "pi", Path: name, Role: role}
			got, err := readFile(sf)
			if transcriptRoles[role] {
				if err != nil || !bytes.Equal(got, body) {
					t.Fatal("transcript contents changed")
				}
			} else {
				if !errors.Is(err, scrub.ErrLiteralSecret) {
					t.Fatal("raw literal assignment allowed")
				}
				if got != nil {
					t.Fatal("withheld read returned unsafe bytes")
				}
			}
		}
	}
}

func TestGuardRunsBeforeWarmBaselineAndNeverLogsSecretBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.sh")
	if err := os.WriteFile(path, []byte("API_KEY=fixture-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := capturepolicy.New("", "")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	s := &Syncer{policy: policy, warm: true, log: slog.New(slog.NewTextHandler(&logs, nil))}
	sf := adapters.SessionFile{Agent: "pi", Root: root, Path: path, Key: "script.sh", Role: adapters.RoleUnknown}
	// No database is supplied: consulting the warm baseline before the guard would panic.
	for range 2 {
		u, err := s.prepare(t.Context(), sf)
		if err != nil || u != nil {
			t.Fatalf("prepare = %v, %v", u, err)
		}
	}
	if s.Summary.Errors != 0 || s.Summary.SkipReasons["literal_secret_assignment"] != 2 {
		t.Fatalf("summary = %+v", s.Summary)
	}
	if logs.Len() != 0 {
		t.Fatal("withheld source unexpectedly logged")
	}
}

func TestDirectorySymlinkSkipsAreVisibleWithoutRetryErrors(t *testing.T) {
	s := &Syncer{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.discoveryIssues(adapters.Discovery{Skipped: []adapters.SkippedFile{{Path: "alias", Reason: "symlink_directory"}, {Path: "missing", Reason: "symlink_unresolved"}}})
	if s.Summary.Errors != 0 || s.Summary.Skipped != 2 || len(s.Summary.SkipReasons) != 2 {
		t.Fatalf("summary = %+v", s.Summary)
	}
}

func TestExcludedMissingExportsDoNotAssertDeletion(t *testing.T) {
	policy, err := capturepolicy.New("", "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Syncer{policy: policy}
	root := t.TempDir()
	for _, name := range []string{"logs_1.sqlite", "logs_1.sqlite.before-upgrade", ".codex-global-state.json.bak", "auth.json.before-upgrade"} {
		sources := []raw.Source{{Path: filepath.Join(root, "missing-export"), SourcePath: filepath.Join(root, name)}}
		absent, err := s.sourcesAbsent(sources)
		if err != nil || absent {
			t.Errorf("sourcesAbsent(%s) = %v, %v, want retained history", name, absent, err)
		}
	}
	absent, err := s.sourcesAbsent([]raw.Source{{Path: filepath.Join(root, "missing-export"), SourcePath: filepath.Join(root, "other.sqlite")}})
	if err != nil || !absent {
		t.Fatalf("sourcesAbsent ordinary database = %v, %v, want absent", absent, err)
	}
}

func TestProjectSnapshotWithholdingAppearsInRunSummary(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Project, "CLAUDE.md"), "API_KEY=fixture-private-value\n")
	s := &Syncer{adapters: adapters.All(e.Home)}
	_, _, _, err := s.stageSnapshot(adapters.SessionRef{Agent: "claude", Key: "fixture"}, e.Claude, e.Project)
	if err != nil {
		t.Fatal(err)
	}
	if s.Summary.Errors != 0 || s.Summary.SkipReasons["literal_secret_assignment"] == 0 {
		t.Fatalf("snapshot summary = %+v", s.Summary)
	}
}

func TestRawGuardRejectsQuotedAndMalformedAssignments(t *testing.T) {
	for name, body := range map[string]string{
		"fishQuotedName": "set -gx 'API_KEY' fixture-secret",
		"fishReordered":  "set -xg API_KEY fixture-secret",
		"fishSplit":      "set -g -x API_KEY fixture-secret",
		"fishLong":       "set --global --export API_KEY fixture-secret",
		"fishAndExport":  "true;and export API_KEY=fixture-secret",
		"fishOrExport":   "false;or export API_KEY=fixture-secret",
		"fishHash":       "set -gx PASSWORD '#fixture-secret'",
		"separator":      "OTHER=ok;API_KEY=fixture-secret",
		"fishSeparator":  "set -gx OTHER ok;set -gx API_KEY fixture-secret",
		"malformed":      "OTHER=ok API_KEY=fixture-secret CMD='",
		"singleQuoted":   "API_KEY='${TOKEN}'",
		"escaped":        `API_KEY=\$TOKEN`,
		"fishQuoted":     "set -gx API_KEY '${TOKEN}'",
		"fishEscaped":    `set -gx API_KEY \$TOKEN`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "script.sh")
			testenv.Write(t, path, body)
			sf := adapters.SessionFile{Agent: "pi", Root: root, Path: path, Key: "script.sh", Role: adapters.RoleUnknown}
			got, err := readFile(sf)
			if !errors.Is(err, scrub.ErrLiteralSecret) || got != nil {
				t.Fatal("readFile did not withhold assignment")
			}
			policy, err := capturepolicy.New("", "")
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			s := &Syncer{policy: policy, warm: true, log: slog.New(slog.NewTextHandler(&logs, nil))}
			u, err := s.prepare(t.Context(), sf)
			if err != nil || u != nil || s.Summary.Errors != 0 || s.Summary.SkipReasons["literal_secret_assignment"] != 1 {
				t.Fatalf("prepare did not report intentional withholding: %+v", s.Summary)
			}
			if logs.Len() != 0 {
				t.Fatal("withheld assignment reached logs")
			}
		})
	}
}

func TestRawGuardKeepsFishReferencesAndUnrelatedCommands(t *testing.T) {
	for name, body := range map[string]string{
		"quotedName":  "set -gx 'API_KEY' $TOKEN",
		"reordered":   "set -xg API_KEY \"${TOKEN}\"",
		"split":       "set -g -x API_KEY $TOKEN",
		"long":        "set --global --export API_KEY $TOKEN",
		"andExport":   "true;and export API_KEY=$TOKEN",
		"orExport":    "false;or export API_KEY=$TOKEN",
		"query":       "set -q API_KEY fixture-value",
		"prose":       "For example, set -xg API_KEY fixture-value",
		"invalidName": "set -gx 'PASSWORD NAME' fixture-value",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "script.sh")
			testenv.Write(t, path, body)
			got, err := readFile(adapters.SessionFile{Agent: "pi", Path: path, Role: adapters.RoleUnknown})
			if err != nil || !bytes.Equal(got, []byte(body)) {
				t.Fatalf("readFile = %v, want unchanged allowed text", err)
			}
		})
	}
}

func TestDeletionFencesUseLogicalSourcePrefixes(t *testing.T) {
	root := t.TempDir()
	fence := filepath.Join(root, "skills", "archive.lock")
	for name, tc := range map[string]struct {
		sources []raw.Source
		reason  string
		want    bool
	}{
		"exact":           {[]raw.Source{{Path: fence}}, "symlink_unresolved", true},
		"child":           {[]raw.Source{{Path: filepath.Join(fence, "source.ts")}}, "symlink_directory", true},
		"siblingPrefix":   {[]raw.Source{{Path: fence + "-sibling.ts"}}, "symlink_directory", false},
		"outside":         {[]raw.Source{{Path: filepath.Join(root, "other.ts")}}, "symlink_directory", false},
		"oneUnknownAlias": {[]raw.Source{{Path: filepath.Join(root, "missing.ts")}, {Path: filepath.Join(fence, "source.ts")}}, "symlink_directory", true},
		"exportSource":    {[]raw.Source{{Path: filepath.Join(root, "staging.sqlite"), SourcePath: filepath.Join(fence, "source.sqlite")}}, "symlink_unresolved", true},
		"stagingOnly":     {[]raw.Source{{Path: filepath.Join(fence, "staging.sqlite"), SourcePath: filepath.Join(root, "source.sqlite")}}, "symlink_directory", false},
		"otherSkip":       {[]raw.Source{{Path: fence}}, "literal_secret_assignment", false},
		"incomparable":    {[]raw.Source{{Path: "relative.ts"}}, "symlink_directory", true},
		"noSources":       {nil, "symlink_directory", false},
	} {
		t.Run(name, func(t *testing.T) {
			got := newDeletionFences([]adapters.SkippedFile{{Path: fence, Reason: tc.reason}}).hasSource(tc.sources)
			if got != tc.want {
				t.Fatalf("hasSource = %v, want %v", got, tc.want)
			}
		})
	}
}
