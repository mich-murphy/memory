package hook

import (
	"bytes"
	"encoding/binary"
)

// parseProcArgs reads kern.procargs2: argc as a 32-bit integer, the executable path, NUL
// padding, then argc NUL-terminated arguments followed by the environment. Arguments are cut
// by count, not by empty strings, so the environment never leaks into argv; only an empty
// argv[0] is indistinguishable from the padding.
func parseProcArgs(buf []byte) ([]string, bool) {
	if len(buf) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(buf))
	_, rest, ok := bytes.Cut(buf[4:], []byte{0})
	if !ok {
		return nil, false
	}
	rest = bytes.TrimLeft(rest, "\x00")
	args := make([]string, 0, min(argc, len(rest)))
	for range argc {
		arg, after, ok := bytes.Cut(rest, []byte{0})
		if !ok {
			return nil, false
		}
		args = append(args, string(arg))
		rest = after
	}
	return cleanArgs(args), true
}
