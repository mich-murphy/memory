package capturesource

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// transformPolicyVersion is bound into every transformed-source fingerprint. Bump it
// whenever scrubber rules, the SQLite backup procedure or the zstd decode limits
// change, so previously fingerprinted sources are re-hinted and re-prepared once.
const transformPolicyVersion = "transform-v1"

// sqliteSidecars are the files a SQLite backup reads besides the database itself.
var sqliteSidecars = []string{"-wal", "-shm", "-journal"}

// transformFingerprint digests every input the queue's transform reads: the source
// stat for all transforms, plus the -wal/-shm/-journal stat identities for a SQLite
// backup (an absent sidecar is itself an identity, so creating or removing one
// changes the result), plus version. Empty means some input is unknown and the
// source must be re-hinted every sweep. Policy and exclusions are not inputs here:
// they are rechecked at capture and delivery.
func transformFingerprint(version, transform, path string, info os.FileInfo) string {
	main := statFingerprint(info)
	if main == "" {
		return ""
	}
	parts := []string{version, transform, main}
	if transform == "sqlite-backup" {
		for _, suffix := range sqliteSidecars {
			side, err := os.Lstat(path + suffix)
			switch {
			case errors.Is(err, os.ErrNotExist):
				parts = append(parts, "absent")
			case err != nil:
				return ""
			default:
				fp := statFingerprint(side)
				if fp == "" {
					return ""
				}
				parts = append(parts, fp)
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// scrubRulesGolden is scrub.RulesDigest() at the time transformPolicyVersion was last
// bumped. TestScrubRulesMatchTransformPolicyVersion fails when they diverge.
const scrubRulesGolden = "e613d5dfa7202909d87d576a64b843a2385a55af725e351aae454819e2877669"
