package capturepolicy

import (
	"path/filepath"
	"testing"
)

func TestCredentialCopyFamiliesDoNotDependOnBackupEncoding(t *testing.T) {
	for _, name := range []string{
		"AUTH.JSON", "Auth.Json.ZIP", "auth.json.backup", "auth.json.~1~",
		"oauth.json-20261014", "mcp-auth.json.tar.gz", ".credentials.json.zst",
		"secret.KEY.BAK", "secret.pem.zip", "CONFIG.ENV.7z",
	} {
		if !IsCredential(name) {
			t.Errorf("credential copy accepted: %s", name)
		}
	}
	for _, name := range []string{"author.json", "auth.jsonnotes", "monkey.txt", "settings.json", "models.json"} {
		if IsCredential(name) {
			t.Errorf("unrelated filename excluded: %s", name)
		}
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "custom-secrets.cfg")
	policy, err := New(config, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".zip", ".backup", "~1~", "-20261014"} {
		if !policy.Excludes([]string{config + suffix}) {
			t.Errorf("active collector config copy accepted: %s", suffix)
		}
	}
	if got := BaseName("Settings.JSON.BAK"); got != "settings.json" {
		t.Errorf("protected config identity = %q", got)
	}
}
