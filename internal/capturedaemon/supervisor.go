package capturedaemon

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// ExecutableReady is activation-only; trusted teardown needs its declared path,
// not an old executable's continued availability.
func ExecutableReady(path string) error {
	if err := SafePath(path); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || !owned(st) || st.Mode().Perm()&0o022 != 0 || st.Mode().Perm()&0o111 == 0 {
		return errors.New("candidate executable unavailable or unsafe")
	}
	return nil
}

func owned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}

// A supervisor file is private but its owned parent may be readable. It must
// never be writable by other users. This is deliberately not WritePrivate's
// secret/config-directory policy, and never chmods an existing directory.
func writeSupervisor(path string, body []byte) error {
	if err := SafePath(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := validateSupervisorParent(path); err != nil {
		return err
	}
	return writePrivateAtomic(path, body)
}

// Validate installed identity before invoking a supervisor or deleting/replacing
// a plan. A private foreign file is not ours merely because its name matches.
func validateSupervisorParent(path string) error {
	st, err := os.Stat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !st.IsDir() || !owned(st) || st.Mode().Perm()&0o022 != 0 {
		return errors.New("unsafe supervisor directory")
	}
	return nil
}

func validateServiceFile(p Plan) error {
	if err := validateSupervisorParent(p.Path); err != nil {
		return err
	}
	st, err := os.Lstat(p.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !st.Mode().IsRegular() || !owned(st) || st.Mode().Perm()&0o077 != 0 || st.Size() > maxConfigBytes {
		return errors.New("unsafe or foreign service plan file")
	}
	body, err := os.ReadFile(p.Path)
	if err != nil || !managedService(p.Path, body) {
		return errors.New("foreign service plan file")
	}
	return nil
}
