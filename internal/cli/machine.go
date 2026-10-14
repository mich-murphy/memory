package cli

import (
	"fmt"

	"github.com/mich-murphy/memory/internal/config"
)

func runMachineID(args []string, o IO) (int, error) {
	fs := newFlags("machine-id", o)
	create := fs.Bool("new", false, "explicitly create an identity for a new machine; never replace an existing identity")
	if err := parse(fs, args); err != nil {
		return exitUsage, err
	}
	if fs.NArg() != 0 {
		o.errorf("agent-sessions machine-id: unexpected arguments\n")
		return exitUsage, errUsage
	}
	var id string
	if *create {
		var err error
		id, err = config.CreateMachineID(config.FilePath())
		if err != nil {
			return exitFailed, err
		}
	} else {
		cfg, err := config.Load(config.FilePath())
		if err != nil {
			return exitFailed, err
		}
		id = cfg.MachineID
	}
	if _, err := fmt.Fprintln(o.Stdout, id); err != nil {
		return exitFailed, fmt.Errorf("machine id: print: %w", err)
	}
	return exitOK, nil
}
