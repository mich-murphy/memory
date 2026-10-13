// Package gitinfo records a repository's git state when a session starts.
package gitinfo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/fsutil"
)

func git(ctx context.Context, dir string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func gitLine(ctx context.Context, dir string, args ...string) string {
	out, err := git(ctx, dir, 10*time.Second, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var (
	userinfoRE = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*://)[^/?#]*@`)
	schemeRE   = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	sshUserRE  = regexp.MustCompile(`^[^@/]+@`)
	portRE     = regexp.MustCompile(`:\d+/`)
	scpRE      = regexp.MustCompile(`^([^/:]+):(\d+/)?`)
)

func stripUserinfo(url string) string {
	return userinfoRE.ReplaceAllString(url, "$1")
}

// normalizeRemote turns https, ssh and scp-style remotes alike into host/owner/repo.
func normalizeRemote(url string) string {
	u := stripUserinfo(strings.TrimSpace(url))
	u = schemeRE.ReplaceAllString(u, "")
	u = sshUserRE.ReplaceAllString(u, "")
	// scp-style host:path, but not host:port/path.
	if m := scpRE.FindStringSubmatch(u); len(m) == 3 && m[2] == "" {
		u = m[1] + "/" + u[len(m[0]):]
	}
	u = portRE.ReplaceAllString(u, "/")
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	host, path, _ := strings.Cut(u, "/")
	return strings.ToLower(host) + "/" + path
}

type State struct {
	Root      string `json:"root,omitempty"`
	CommonDir string `json:"common_dir,omitempty"`
	Remote    string `json:"remote,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Head      string `json:"head,omitempty"`
	Dirty     *bool  `json:"dirty,omitempty"`
	RepoID    string `json:"repo_id,omitempty"`
}

func (s State) IsZero() bool {
	return s == State{}
}

func Read(ctx context.Context, cwd, machineID string) State {
	if cwd == "" || !fsutil.IsDir(cwd) {
		return State{}
	}
	root := gitLine(ctx, cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		return State{}
	}
	common := gitLine(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	remote := gitLine(ctx, cwd, "remote", "get-url", "origin")
	g := State{
		Root:      root,
		CommonDir: common,
		Remote:    stripUserinfo(remote),
		Head:      gitLine(ctx, cwd, "rev-parse", "HEAD"),
		Branch:    gitLine(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD"),
	}
	if status, err := git(ctx, cwd, 20*time.Second, "status", "--porcelain", "--untracked-files=no"); err == nil {
		dirty := len(bytes.TrimSpace(status)) > 0
		g.Dirty = &dirty
	}
	key := machineID + ":" + root
	if common != "" {
		key = machineID + ":" + common
	}
	if remote != "" {
		key = normalizeRemote(remote)
	}
	sum := sha256.Sum256([]byte(key))
	g.RepoID = hex.EncodeToString(sum[:])[:16]
	return g
}
