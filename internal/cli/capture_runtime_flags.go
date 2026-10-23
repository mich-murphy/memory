package cli

import (
	"encoding/hex"
	"errors"
)

func validateRuntimeFlags(cmd string, f runtimeFlags, extra int) error {
	if extra != 0 || f.config == "" || f.after < 0 {
		return errors.New("invalid capture runtime arguments; --config required")
	}
	switch cmd {
	case "replay":
		if f.id <= 0 {
			return errors.New("positive quarantine coordinate required")
		}
	case "optional-quota-replay":
		if f.session == "" || (f.agent != "claude" && f.agent != "codex" && f.agent != "pi") {
			return errors.New("optional quota replay requires exact --agent and --session")
		}
	case "enrichment-replay", "optional-replay":
		if !runtimeWorkID(f.workID) {
			return errors.New("valid enrichment work coordinate required")
		}
	case "enrichment-status", "optional-status":
		if f.workAfter != "" && !runtimeWorkID(f.workAfter) {
			return errors.New("valid enrichment work cursor required")
		}
		fallthrough
	case "work-status", "quarantine":
		if f.limit < 1 || f.limit > 256 {
			return errors.New("capture status page must be 1..256")
		}
	}
	return nil
}

func runtimeWorkID(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
