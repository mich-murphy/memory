# Merge Go text coverage profiles (unit tests + end-to-end covdata) into one:
# one "mode:" line, then each block once with its counts summed.
/^mode:/ { if (!mode) { print; mode = 1 } next }
NF == 3 {
	k = $1 " " $2
	if (!(k in n)) { order[++m] = k }
	n[k] += $3
}
END { for (i = 1; i <= m; i++) print order[i], n[order[i]] }
