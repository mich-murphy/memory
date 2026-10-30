package capturepolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialImportsAndCustomRootsStayExcluded(t *testing.T) {
	for _, path := range []string{"/home/u/.ssh/id_rsa", "/home/u/.ssh/id_ed25519.pub", "/home/u/.gnupg/private-keys-v1.d/key", "/home/u/.aws/config", "/home/u/.azure/accessTokens.json", "/home/u/.kube/config", "/home/u/.config/gcloud/credentials.db", "/project/.netrc", "/project/.npmrc", "/project/.pypirc", "/project/.git-credentials", "/other/id_ed25519.agent-sessions-backup-123"} {
		if !IsCredential(path) {
			t.Fatal("known credential input admitted")
		}
	}
	for _, path := range []string{"/home/u/.ssh-not/CLAUDE.md", "/project/kube/AGENTS.md", "/project/configuration.json"} {
		if IsCredential(path) {
			t.Fatal("unrelated ordinary source withheld")
		}
	}
	root := t.TempDir()
	secret := filepath.Join(root, ".ssh", "private")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("synthetic key"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "instructions.md")
	if err := os.Symlink(secret, alias); err != nil {
		t.Fatal(err)
	}
	resolution, err := Resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excludes(resolution.Identities) {
		t.Fatal("alias hid credential directory")
	}
}
