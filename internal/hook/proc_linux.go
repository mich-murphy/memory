package hook

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

func proc(pid int) (argv []string, ppid int, ok bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	cmdline, err := os.ReadFile(dir + "/cmdline")
	if err != nil {
		return nil, 0, false
	}
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return nil, 0, false
	}
	ppid, ok = statPPID(stat)
	if !ok {
		return nil, 0, false
	}
	return splitCmdline(cmdline), ppid, true
}

// The command name in parentheses may itself contain ") ".
func statPPID(stat []byte) (int, bool) {
	_, afterName, ok := bytes.CutLast(stat, []byte(")"))
	if !ok {
		return 0, false
	}
	fields := strings.Fields(string(afterName))
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	return ppid, err == nil
}

func splitCmdline(data []byte) []string {
	return cleanArgs(strings.Split(string(data), "\x00"))
}
