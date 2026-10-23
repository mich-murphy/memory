// Package capturehooks plans opt-in capture-v1 hooks independently of legacy install.
package capturehooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/shellwords"
)

const (
	Marker           = "agent-sessions-capture-v1"
	commandPrefix    = ": " + Marker + "; exec "
	piPrefix         = "// " + Marker + ": owned extension\n"
	maxSettingsBytes = 4 << 20
)

const (
	maxPlanBytes = 16 << 20
	maxRoots     = 128
)

type Root = capturedaemon.Root

type Options struct {
	Home       string
	Roots      []Root
	Executable string
	ConfigPath string
	Remove     bool
}

// Change deliberately exposes facts, never settings/extension contents.
type Change struct {
	Path         string   `json:"path"`
	Agent        string   `json:"agent"`
	BeforeSHA256 string   `json:"before_sha256,omitempty"`
	AfterSHA256  string   `json:"after_sha256,omitempty"`
	Existed      bool     `json:"existed"`
	Delete       bool     `json:"delete"`
	Changed      bool     `json:"changed"`
	Command      []string `json:"command,omitempty"`
}

type Plan struct {
	Changes             []Change `json:"changes"`
	RemainingAcceptance []string `json:"remaining_acceptance"`
	entries             []entry
	options             Options
	valid               bool
}

type entry struct {
	path           string
	before, after  []byte
	exists, remove bool
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Build reads only explicitly supplied paths. Nil/empty Roots selects canonical
// home defaults; nonempty Roots is the complete selected inventory (no env lookup).
func Build(o Options) (Plan, error) {
	p := Plan{options: o}
	if len(o.Roots) > maxRoots {
		return Plan{}, errors.New("hook root inventory exceeds limit")
	}
	if err := validateOptions(o); err != nil {
		return Plan{}, err
	}
	roots := o.Roots
	if len(roots) == 0 {
		roots = []Root{{Agent: "claude", Path: filepath.Join(o.Home, ".claude")}, {Agent: "codex", Path: filepath.Join(o.Home, ".codex")}, {Agent: "pi", Path: filepath.Join(o.Home, ".pi", "agent")}}
	}
	totalBytes := 0
	seen := map[string]bool{}
	for _, root := range roots {
		if err := capturedaemon.SafePath(root.Path); err != nil {
			return Plan{}, err
		}
		path, err := target(root)
		if err != nil {
			return Plan{}, err
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		e, fact, err := planTarget(o, root, path, maxPlanBytes-totalBytes)
		if err != nil {
			return Plan{}, err
		}

		totalBytes += len(e.before) + len(e.after)
		if totalBytes > maxPlanBytes {
			return Plan{}, errors.New("hook plan byte budget exceeded; select fewer roots")
		}
		p.Changes = append(p.Changes, fact)
		p.entries = append(p.entries, e)
		if root.Agent == "codex" && !o.Remove {
			p.RemainingAcceptance = append(p.RemainingAcceptance, "Codex native /hooks approval and hash-trust acceptance required; trust state was not changed")
		}
		if root.Agent == "pi" && !o.Remove {
			p.RemainingAcceptance = append(p.RemainingAcceptance, "Pi reload/new session and native event acceptance required")
		}
	}
	p.valid = true
	return p, nil
}

func planTarget(o Options, root Root, path string, remaining int) (entry, Change, error) {
	if path == o.ConfigPath || path == o.Executable {
		return entry{}, Change{}, errors.New("hook target overlaps protected input")
	}
	before, exists, err := readTarget(path)
	if err != nil {
		return entry{}, Change{}, err
	}
	if len(before) > remaining {
		return entry{}, Change{}, errors.New("hook plan byte budget exceeded; select fewer roots")
	}
	argv := []string{o.Executable, "capture-v1", "hook", "--config", o.ConfigPath, "--agent", root.Agent}
	var after []byte
	remove := false
	if root.Agent == "pi" {
		if exists && !bytes.HasPrefix(before, []byte(piPrefix)) {
			return entry{}, Change{}, errors.New("candidate extension is not owned")
		}
		if o.Remove {
			remove = exists
		} else {
			after, err = piExtension(argv)
		}
	} else if exists || !o.Remove {
		after, err = editJSON(before, exists, root.Agent, commandPrefix+shellwords.Join(argv), o.Remove)
	}
	if err != nil {
		return entry{}, Change{}, err
	}
	if len(after) > maxSettingsBytes {
		return entry{}, Change{}, errors.New("generated hook settings exceed size limit")
	}
	e := entry{path: path, before: before, after: after, exists: exists, remove: remove}
	changed := remove || !bytes.Equal(before, after)
	fact := Change{Path: path, Agent: root.Agent, Existed: exists, Delete: remove, Changed: changed}
	if exists {
		fact.BeforeSHA256 = digest(before)
	}
	if after != nil {
		fact.AfterSHA256 = digest(after)
	}
	if !o.Remove {
		fact.Command = argv
	}
	return e, fact, nil
}

func target(root Root) (string, error) {
	switch root.Agent {
	case "claude":
		return filepath.Join(root.Path, "settings.json"), nil
	case "codex":
		return filepath.Join(root.Path, "hooks.json"), nil
	case "pi":
		return filepath.Join(root.Path, "extensions", Marker+".ts"), nil
	default:
		return "", errors.New("unknown capture hook agent")
	}
}

func validateOptions(o Options) error {
	for _, path := range []string{o.Home, o.Executable, o.ConfigPath} {
		if err := capturedaemon.SafePath(path); err != nil {
			return err
		}
	}
	st, err := os.Stat(o.Home)
	if err != nil || !st.IsDir() {
		return errors.New("hook home unavailable")
	}
	st, err = os.Stat(o.ConfigPath)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		return errors.New("hook config must be a private regular file")
	}
	st, err = os.Stat(o.Executable)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o022 != 0 || st.Mode().Perm()&0o111 == 0 {
		return errors.New("hook executable must be a non-writable regular executable")
	}
	return nil
}

func readTarget(path string) ([]byte, bool, error) {
	if err := capturedaemon.SafePath(path); err != nil {
		return nil, false, err
	}
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxSettingsBytes {
		return nil, false, errors.New("hook settings unavailable, unsafe or oversized")
	}
	// Existing native settings may be 0644; all new settings and backups are 0600.
	f, err := os.Open(path)
	if err != nil {
		return nil, false, errors.New("hook settings unreadable")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxSettingsBytes+1))
	err = errors.Join(err, f.Close())
	if err != nil || len(b) > maxSettingsBytes {
		return nil, false, errors.New("hook settings unreadable or oversized")
	}
	return b, true, nil
}
