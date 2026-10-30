package capturedaemon

import (
	"encoding/xml"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const serviceDescription = "Experimental local session capture (not production cutover)"

// Accept only our generated grammar, not general plist/systemd semantics. Old
// executable/config paths and supported numeric restart tuning may differ from
// the desired plan. Every other byte must match the current safe renderer: an
// unknown execution directive, duplicate/reset, or ambiguous spelling is foreign.
func managedService(path string, body []byte) bool {
	platform := "linux"
	var args []string
	var err error
	switch filepath.Ext(path) {
	case ".plist":
		platform = "darwin"
		args, err = plistServiceArgs(body)
	case ".service":
		args, err = systemdServiceArgs(body)
	default:
		return false
	}
	if err != nil || len(args) != 5 || args[1] != "capture-v1" || args[2] != "run" || args[3] != "--config" || filepath.Base(path) != ServiceName+filepath.Ext(path) {
		return false
	}
	// ServicePlan validates the old paths without requiring their files to exist.
	generated, err := ServicePlan(platform, filepath.Dir(path), args[0], args[4], 0)
	return err == nil && normalizeServiceTuning(platform, string(body)) == generated.Unit
}

// Tuning accepts canonical positive decimal integers only, at most six digits.
// Permissions, launch type, restart disposition, environment and all execution
// directives are fixed by the renderer, never interpreted or normalized here.
func normalizeServiceTuning(platform, body string) string {
	if platform == "darwin" {
		return launchdThrottle.ReplaceAllString(body, "<key>ThrottleInterval</key><integer>300</integer>")
	}
	for _, tuning := range systemdTuning {
		body = tuning.pattern.ReplaceAllString(body, tuning.generated)
	}
	return body
}

var (
	launchdThrottle = regexp.MustCompile(`<key>ThrottleInterval</key><integer>[1-9][0-9]{0,5}</integer>`)
	systemdTuning   = []struct {
		pattern   *regexp.Regexp
		generated string
	}{
		{regexp.MustCompile(`(?m)^StartLimitIntervalSec=[1-9][0-9]{0,5}$`), "StartLimitIntervalSec=3600"},
		{regexp.MustCompile(`(?m)^StartLimitBurst=[1-9][0-9]{0,5}$`), "StartLimitBurst=5"},
		{regexp.MustCompile(`(?m)^RestartSec=[1-9][0-9]{0,5}$`), "RestartSec=60"},
		{regexp.MustCompile(`(?m)^RestartSteps=[1-9][0-9]{0,5}$`), "RestartSteps=4"},
		{regexp.MustCompile(`(?m)^RestartMaxDelaySec=[1-9][0-9]{0,5}$`), "RestartMaxDelaySec=900"},
	}
)

var (
	plistArgv = regexp.MustCompile(`<key>ProgramArguments</key><array>((?:<string>[^<]*</string>){5})</array>`)
	plistArg  = regexp.MustCompile(`<string>[^<]*</string>`)
)

// Extract only the generated string slots. Whole-unit equality above rejects
// all other XML, including Program overrides and extra dictionaries/attributes.
func plistServiceArgs(body []byte) ([]string, error) {
	matches := plistArgv.FindAllSubmatch(body, -1)
	if len(matches) != 1 {
		return nil, errors.New("invalid generated service arguments")
	}
	var args []string
	for _, word := range plistArg.FindAll(matches[0][1], -1) {
		var value string
		if err := xml.Unmarshal(word, &value); err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	return args, nil
}

var (
	systemdArgv       = regexp.MustCompile(`(?m)^ExecStart=(.*)$`)
	quotedServiceWord = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

func systemdServiceArgs(body []byte) ([]string, error) {
	matches := systemdArgv.FindAllSubmatch(body, -1)
	if len(matches) != 1 {
		return nil, errors.New("invalid generated service command")
	}
	command := string(matches[0][1])
	words := quotedServiceWord.FindAllString(command, -1)
	if len(words) != 5 || strings.Join(words, " ") != command {
		return nil, errors.New("invalid service command")
	}
	args := make([]string, len(words))
	for n, word := range words {
		value, err := strconv.Unquote(word)
		if err != nil {
			return nil, err
		}
		value = strings.ReplaceAll(strings.ReplaceAll(value, "%%", "%"), "$$", "$")
		if systemdQuote(value) != word {
			return nil, errors.New("invalid service quoting")
		}
		args[n] = value
	}
	return args, nil
}

// The CLI qualifies the desired config/policy and executable before Apply.
// Start/restart do not install the desired unit, so bind activation to those
// exact qualified argv coordinates, while still permitting safe tuning drift.
func validateActivationCoordinates(p Plan, installed []byte) error {
	var oldArgs, newArgs []string
	var oldErr, newErr error
	if filepath.Ext(p.Path) == ".plist" {
		oldArgs, oldErr = plistServiceArgs(installed)
		newArgs, newErr = plistServiceArgs([]byte(p.Unit))
	} else {
		oldArgs, oldErr = systemdServiceArgs(installed)
		newArgs, newErr = systemdServiceArgs([]byte(p.Unit))
	}
	if oldErr != nil || newErr != nil || !slices.Equal(oldArgs, newArgs) {
		return errors.New("installed service execution differs; install qualified plan before activation")
	}
	return nil
}
