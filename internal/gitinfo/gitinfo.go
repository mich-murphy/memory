// Package gitinfo records git state at session start and exports the repository history the
// normaliser needs.
//
//	git/<repo_id>/repo.json           the remote (checkouts are in each session's manifest)
//	git/<repo_id>/log/<head>.txt.gz   `git log --name-status` for commits not in an earlier export
//	git/<repo_id>/tree/<head>.txt.gz  `git ls-tree -r -l <head>`: every file and blob at that commit
//
// Log records are separated by \x1e; each starts with NUL-separated fields (sha, parents, author
// name, author email, author date, committer date, full message) followed by the --name-status
// lines. repo_id is a hash of the normalised remote URL, or of the machine and the common git
// dir when there is no remote, so clones of one repo on many machines share it.
package gitinfo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/state"
)

const logFormat = "%x1e%H%x00%P%x00%an%x00%ae%x00%aI%x00%cI%x00%B%x00"

// ErrGit means a git command failed.
var ErrGit = errors.New("git failed")

func git(ctx context.Context, dir string, timeout time.Duration, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: git %s: %w", ErrGit, strings.Join(args, " "), err)
	}
	return out, nil
}

func gitLine(ctx context.Context, dir string, args ...string) string {
	out, err := git(ctx, dir, 10*time.Second, nil, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var (
	userinfoRE = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*://)[^/@]*@`)
	schemeRE   = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	sshUserRE  = regexp.MustCompile(`^[^@/]+@`)
	portRE     = regexp.MustCompile(`:\d+/`)
	scpRE      = regexp.MustCompile(`^([^/:]+):(\d+/)?`)
)

// StripUserinfo drops a token or password embedded in a remote URL (a credential, never stored).
func StripUserinfo(url string) string {
	return userinfoRE.ReplaceAllString(url, "$1")
}

// NormaliseRemote turns https, ssh and scp-style remotes alike into host/owner/repo.
func NormaliseRemote(url string) string {
	u := StripUserinfo(strings.TrimSpace(url))
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

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// State is the git state of the repository containing cwd, or a zero value outside one.
func State(ctx context.Context, cwd, machineID string) state.GitState {
	if cwd == "" || !isDir(cwd) {
		return state.GitState{}
	}
	root := gitLine(ctx, cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		return state.GitState{}
	}
	common := gitLine(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	remote := gitLine(ctx, cwd, "remote", "get-url", "origin")
	g := state.GitState{
		Root:      root,
		CommonDir: common,
		Remote:    StripUserinfo(remote),
		Head:      gitLine(ctx, cwd, "rev-parse", "HEAD"),
		Branch:    gitLine(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD"),
	}
	if status, err := git(ctx, cwd, 20*time.Second, nil, "status", "--porcelain", "--untracked-files=no"); err == nil {
		dirty := len(bytes.TrimSpace(status)) > 0
		g.Dirty = &dirty
	}
	key := machineID + ":" + root
	if common != "" {
		key = machineID + ":" + common
	}
	if remote != "" {
		key = NormaliseRemote(remote)
	}
	sum := sha256.Sum256([]byte(key))
	g.RepoID = hex.EncodeToString(sum[:])[:16]
	return g
}

// Object is one object to upload: key and body.
type Object struct {
	Key  string
	Body []byte
}

// Export returns the objects to upload for g's HEAD, leaving out commits reachable from heads
// exported before.
func Export(ctx context.Context, g state.GitState, exportedHeads []string) ([]Object, error) {
	if g.Root == "" || g.Head == "" || g.RepoID == "" {
		return nil, nil
	}
	// Only exclude earlier heads this clone still has; an unknown sha would fail the log.
	var known []string
	if len(exportedHeads) > 0 {
		out, err := git(ctx, g.Root, 30*time.Second, []byte(strings.Join(exportedHeads, "\n")+"\n"), "cat-file", "--batch-check")
		if err == nil {
			for line := range strings.SplitSeq(string(out), "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[1] == "commit" {
					known = append(known, f[0])
				}
			}
		}
	}
	args := []string{"log", "--name-status", "--format=" + logFormat, g.Head}
	for _, h := range known {
		args = append(args, "^"+h)
	}
	log, err := git(ctx, g.Root, 5*time.Minute, nil, args...)
	if err != nil {
		return nil, err
	}
	tree, err := git(ctx, g.Root, 2*time.Minute, nil, "ls-tree", "-r", "-l", g.Head)
	if err != nil {
		return nil, err
	}
	logGz, err := gzipBytes(log)
	if err != nil {
		return nil, err
	}
	treeGz, err := gzipBytes(tree)
	if err != nil {
		return nil, err
	}
	repo := map[string]any{"repo_id": g.RepoID, "remote": nilIfEmpty(g.Remote)}
	repoJSON, err := jsonx.Indent(repo, 1)
	if err != nil {
		return nil, err
	}
	prefix := "git/" + g.RepoID + "/"
	return []Object{
		{Key: prefix + "log/" + g.Head + ".txt.gz", Body: logGz},
		{Key: prefix + "tree/" + g.Head + ".txt.gz", Body: treeGz},
		{Key: prefix + "repo.json", Body: bytes.TrimSuffix(repoJSON, []byte("\n"))},
	}, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if _, err := zw.Write(data); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return buf.Bytes(), nil
}
