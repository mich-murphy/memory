// Package config loads settings from environment variables and an optional env file.
//
// Hooks run without the user's shell profile, so credentials can also live in
// ~/.config/agent-sessions/config.env (KEY=VALUE lines). Real environment variables win over
// the file.
package config

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Config is everything a run needs to know about this machine and the bucket.
type Config struct {
	MachineID  string
	Hostname   string
	StateDir   string
	Home       string
	ConfigPath string

	R2Endpoint        string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Bucket          string

	// LocalStore writes to a local directory instead of R2 (testing, or a mounted share).
	LocalStore string
	// DBIntervalMinutes is the minimum time between SQLite copies of one database.
	DBIntervalMinutes int
}

// Home is the user's home directory, from $HOME first so tests and hooks agree.
func Home() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

// FilePath is the env file: $AGENT_SESSIONS_CONFIG, or ~/.config/agent-sessions/config.env.
func FilePath() string {
	if p := os.Getenv("AGENT_SESSIONS_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".config", "agent-sessions", "config.env")
}

// DefaultStateDir is where state, logs and temp files live unless AGENT_SESSIONS_STATE_DIR says
// otherwise.
func DefaultStateDir() string {
	return filepath.Join(Home(), ".local", "state", "agent-sessions")
}

// StateDir is the state directory from the environment alone, for the hook's fast path.
func StateDir() string {
	if d := os.Getenv("AGENT_SESSIONS_STATE_DIR"); d != "" {
		return d
	}
	return DefaultStateDir()
}

// Load reads the env file at path (FilePath() when empty) and the environment.
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
		StateDir:          get("AGENT_SESSIONS_STATE_DIR"),
		Home:              Home(),
		ConfigPath:        path,
		R2Endpoint:        get("R2_ENDPOINT"),
		R2AccessKeyID:     get("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: get("R2_SECRET_ACCESS_KEY"),
		R2Bucket:          get("R2_BUCKET"),
		LocalStore:        get("AGENT_SESSIONS_LOCAL_STORE"),
		DBIntervalMinutes: 60,
	}
	if cfg.StateDir == "" {
		cfg.StateDir = DefaultStateDir()
	}
	if n, err := strconv.Atoi(get("AGENT_SESSIONS_DB_INTERVAL_MINUTES")); err == nil {
		cfg.DBIntervalMinutes = max(0, n)
	}
	if override := get("AGENT_SESSIONS_MACHINE_ID"); override != "" {
		cfg.MachineID = sanitize(override)
	} else if cfg.MachineID, err = MachineUUID(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return cfg, nil
}

// MissingR2Settings names the R2 settings that are empty.
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

// MachineUUID is this machine's id in object keys: a UUID made on first use and kept in
// machine-id next to config.env. Hostnames collide (two laptops called "macbook", VMs rebuilt
// from one image), so they are only a label in manifests.
func MachineUUID(configDir string) (string, error) {
	path := filepath.Join(configDir, "machine-id")
	if data, err := os.ReadFile(path); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return sanitize(v), nil
		}
	}
	id := newUUID()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// Another process won the race; use its id.
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", fmt.Errorf("machine id: %w", rerr)
		}
		if v := strings.TrimSpace(string(data)); v != "" {
			return sanitize(v), nil
		}
		return id, nil
	}
	if err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	if _, err := f.WriteString(id + "\n"); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("machine id: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	return id, nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
