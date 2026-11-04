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
//
// The scrub digest guard covers only the rule table. scrubDoc/IsSecretName and other
// scrubbing logic outside it, the SQLite backup procedure and zstd limits are not
// covered and still need a manual bump.
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
const scrubRulesGolden = "95003ab929dd2b7225959f4bbd268e01320b347a4931ed8fded129333776b606"
