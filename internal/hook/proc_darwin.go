package hook

import "golang.org/x/sys/unix"

func proc(pid int) (argv []string, ppid int, ok bool) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return nil, 0, false
	}
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, 0, false
	}
	argv, ok = parseProcArgs(args)
	if !ok {
		return nil, 0, false
	}
	return argv, int(info.Eproc.Ppid), true
}
