// Package config loads settings from environment variables and an env file. Hooks run without
// the user's shell profile, so the file is how they get credentials.
package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"uuid"

	"github.com/mich-murphy/memory/internal/fsutil"
)

type Config struct {
	MachineID string
	Hostname  string
	StateDir  string
	Home      string
	Path      string

	R2Endpoint        string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Bucket          string

	LocalStore string
}

// Home falls back to / so a hook run without $HOME still never fails.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

func FilePath() string {
	if p := os.Getenv("AGENT_SESSIONS_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".config", "agent-sessions", "config.env")
}

func defaultStateDir() string {
	return filepath.Join(Home(), ".local", "state", "agent-sessions")
}

// StateDir skips the env file, for the hook's fast path, so every run reads it this way: the hook
// leaves its markers and launches where the sync looks for them.
func StateDir() string {
	if d := os.Getenv("AGENT_SESSIONS_STATE_DIR"); d != "" {
		return d
	}
	return defaultStateDir()
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = FilePath()
	}
	file, err := readEnvFile(path)
	if err != nil {
		return nil, err
	}
	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return file[key]
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	cfg := &Config{
		Hostname:          hostname,
		StateDir:          StateDir(),
		Home:              Home(),
		Path:              path,
		R2Endpoint:        get("R2_ENDPOINT"),
		R2AccessKeyID:     get("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: get("R2_SECRET_ACCESS_KEY"),
		R2Bucket:          get("R2_BUCKET"),
		LocalStore:        get("AGENT_SESSIONS_LOCAL_STORE"),
	}
	if override := get("AGENT_SESSIONS_MACHINE_ID"); override != "" {
		cfg.MachineID = sanitize(override)
	} else if cfg.MachineID, err = MachineUUID(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) MissingR2Settings() []string {
	var missing []string
	for _, kv := range []struct{ name, value string }{
		{"R2_ENDPOINT", c.R2Endpoint},
		{"R2_ACCESS_KEY_ID", c.R2AccessKeyID},
		{"R2_SECRET_ACCESS_KEY", c.R2SecretAccessKey},
		{"R2_BUCKET", c.R2Bucket},
	} {
		if kv.value == "" {
			missing = append(missing, kv.name)
		}
	}
	return missing
}

func readEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'') {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return values, nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitize(name string) string {
	s := strings.Trim(unsafeChars.ReplaceAllString(name, "-"), "-")
	if s == "" {
		return "unknown"
	}
	return s
}

// MissingMachineIDError requires an operator choice: a missing identity might be a lost
// identity, not a fresh install. Hostnames cannot safely identify an old archive namespace.
type MissingMachineIDError struct {
	Path  string
	Empty bool
}

func (e *MissingMachineIDError) Error() string {
	return fmt.Sprintf("config: machine identity missing or empty at %s; restore the original machine-id file or set AGENT_SESSIONS_MACHINE_ID to the known previous ID; only for a deliberately new machine, run `agent-sessions machine-id --new` (an empty existing file must first be deliberately removed)", e.Path)
}

// MachineUUID reads the persistent identity used in object keys instead of the hostname.
// It never creates or replaces an identity, including during install or config loading.
func MachineUUID(configDir string) (string, error) {
	return readMachineID(filepath.Join(configDir, "machine-id"))
}

// CreateMachineID is exclusively for an explicit operator request to start a new machine.
// An existing identity (including an env/file override) is returned, never rotated. An empty
// existing file is refused: the operator must restore it or deliberately remove it first.
func CreateMachineID(configPath string) (string, error) {
	cfg, err := Load(configPath)
	if err == nil {
		return cfg.MachineID, nil
	}
	var missing *MissingMachineIDError
	if !errors.As(err, &missing) {
		return "", err
	}
	if missing.Empty {
		return "", fmt.Errorf("config: refusing to replace an existing machine-id file: %w", err)
	}
	// Written whole under another name and linked exclusively into place, so simultaneous
	// explicit requests all read one id and a crash cannot leave the identity file empty.
	id := uuid.New().String()
	tmp := missing.Path + "." + id
	if err := fsutil.WriteFileAtomic(tmp, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }() // a leftover holds only an unused id
	if err := os.Link(tmp, missing.Path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("machine id: %w", err)
	}
	return readMachineID(missing.Path)
}

func readMachineID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", &MissingMachineIDError{Path: path}
	}
	if err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	if id := strings.TrimSpace(string(data)); id != "" {
		return sanitize(id), nil
	}
	return "", &MissingMachineIDError{Path: path, Empty: true}
}
