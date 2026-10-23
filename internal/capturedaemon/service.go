package capturedaemon

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ServiceName = "agent-sessions-capture-experimental"

type Command struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}
type Plan struct {
	Path    string    `json:"path"`
	Unit    string    `json:"unit"`
	Start   []Command `json:"start"`
	Stop    []Command `json:"stop"`
	Restart []Command `json:"restart"`
}

// Runner is injected. Merely planning a service never executes host commands.
type Runner func(context.Context, string, ...string) error

func ServicePlan(platform, home, executable, configPath string, uid int) (Plan, error) {
	var p Plan
	for _, path := range []string{home, executable, configPath} {
		if err := SafePath(path); err != nil {
			return p, err
		}
	}
	if uid < 0 {
		return p, errors.New("invalid service user")
	}
	args := []string{executable, "capture-v1", "run", "--config", configPath}
	switch platform {
	case "darwin":
		p.Path = filepath.Join(home, "Library", "LaunchAgents", ServiceName+".plist")
		var b strings.Builder
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>" + ServiceName + "</string><key>ProgramArguments</key><array>")
		for _, arg := range args {
			b.WriteString("<string>")
			if err := xml.EscapeText(&b, []byte(arg)); err != nil {
				return p, err
			}
			b.WriteString("</string>")
		}
		b.WriteString("</array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ProcessType</key><string>Background</string><key>Umask</key><integer>63</integer></dict></plist>\n")
		p.Unit = b.String()
		domain := "gui/" + strconv.Itoa(uid)
		p.Start = []Command{{"launchctl", []string{"bootstrap", domain, p.Path}}}
		p.Stop = []Command{{"launchctl", []string{"bootout", domain, p.Path}}}
		p.Restart = []Command{{"launchctl", []string{"kickstart", "-k", domain + "/" + ServiceName}}}
	case "linux":
		p.Path = filepath.Join(home, ".config", "systemd", "user", ServiceName+".service")
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = systemdQuote(a)
		}
		p.Unit = "[Unit]\nDescription=Experimental local session capture (not production cutover)\n[Service]\nType=simple\nExecStart=" + strings.Join(quoted, " ") + "\nRestart=on-failure\nRestartSec=5\nUMask=0077\n[Install]\nWantedBy=default.target\n"
		p.Start = []Command{{"systemctl", []string{"--user", "daemon-reload"}}, {"systemctl", []string{"--user", "enable", "--now", ServiceName + ".service"}}}
		p.Stop = []Command{{"systemctl", []string{"--user", "disable", "--now", ServiceName + ".service"}}}
		p.Restart = []Command{{"systemctl", []string{"--user", "restart", ServiceName + ".service"}}}
	default:
		return p, errors.New("experimental service supports only darwin and linux")
	}
	return p, nil
}

func systemdQuote(s string) string {
	// systemd applies specifier and environment expansion even inside quotes.
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "$", "$$")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return "\"" + s + "\""
}

// Apply is the sole host-mutation boundary. Dry runs do not stat/write/execute.
// A failed install keeps its plan file for diagnosis; uninstall never removes
// config, spool, original sources, or policy. Stop failure prevents deletion.
func Apply(ctx context.Context, p Plan, action string, apply bool, run Runner) error {
	commands, err := serviceCommands(p, action)
	if err != nil || !apply {
		return err
	}
	if run == nil {
		return errors.New("service apply requires explicit command runner")
	}
	if err := SafePath(p.Path); err != nil {
		return err
	}
	if action == "install" {
		if err := WritePrivate(p.Path, []byte(p.Unit)); err != nil {
			return err
		}
	}
	for _, cmd := range commands {
		if err := run(ctx, cmd.Name, cmd.Args...); err != nil {
			return fmt.Errorf("experimental service %s command failed", action)
		}
	}
	if action == "uninstall" {
		return removeServicePlan(ctx, p.Path, run)
	}
	return nil
}

func serviceCommands(p Plan, action string) ([]Command, error) {
	switch action {
	case "install", "start":
		return p.Start, nil
	case "uninstall", "stop":
		return p.Stop, nil
	case "restart":
		return p.Restart, nil
	default:
		return nil, errors.New("service action must be install, uninstall, start, stop or restart")
	}
}

func removeServicePlan(ctx context.Context, path string, run Runner) error {
	st, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
			return errors.New("unsafe service plan file")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	// Fence the directory even on a retry after an uncertain prior deletion.
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err := errors.Join(d.Sync(), d.Close()); err != nil {
		return err
	}
	if strings.HasSuffix(path, ".service") {
		if err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return errors.New("experimental service reload failed")
		}
	}
	return nil
}
