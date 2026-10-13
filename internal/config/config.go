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
)

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

func DefaultStateDir() string {
	return filepath.Join(Home(), ".local", "state", "agent-sessions")
}

// StateDir skips the env file, for the hook's fast path.
func StateDir() string {
	if d := os.Getenv("AGENT_SESSIONS_STATE_DIR"); d != "" {
		return d
	}
	return DefaultStateDir()
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
		StateDir:          get("AGENT_SESSIONS_STATE_DIR"),
		Home:              Home(),
		ConfigPath:        path,
		R2Endpoint:        get("R2_ENDPOINT"),
		R2AccessKeyID:     get("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: get("R2_SECRET_ACCESS_KEY"),
		R2Bucket:          get("R2_BUCKET"),
		LocalStore:        get("AGENT_SESSIONS_LOCAL_STORE"),
	}
	if cfg.StateDir == "" {
		cfg.StateDir = DefaultStateDir()
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

// MachineUUID is used in object keys instead of the hostname, because hostnames collide (two
// laptops called "macbook", VMs rebuilt from one image).
func MachineUUID(configDir string) (string, error) {
	path := filepath.Join(configDir, "machine-id")
	if data, err := os.ReadFile(path); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return sanitize(v), nil
		}
	}
	id := uuid.New().String()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
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
