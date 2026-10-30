package publication

import (
	"errors"
	"strconv"
	"strings"
)

// IsSelected reports whether a source declares an object dependency. ValidateManifest
// must still succeed before the source is trusted.
func IsSelected(s Source) bool { return s.Withheld == nil }

// SourceKeyHash returns the language-neutral effective source identity.
func SourceKeyHash(s Source) string {
	if s.Withheld != nil {
		return s.Withheld.SourceIDHash
	}
	return token([]byte(s.SourceID))
}

// ObjectCommitment binds the exact descriptor using NUL-separated UTF-8 fields.
// It does not validate the descriptor; callers must validate before trusting it.
func ObjectCommitment(o Object) string {
	return token([]byte(strings.Join([]string{o.Key, o.SHA256, o.EncodedSHA256, o.Encoding, strconv.FormatInt(o.Size, 10), strconv.FormatInt(o.EncodedSize, 10)}, "\x00")))
}

func sourceCommitment(s Source) string {
	if s.Withheld != nil {
		return s.Withheld.SnapshotCommitment
	}
	return ObjectCommitment(s.Object)
}

func validWithheldReason(reason string) bool {
	return reason == "policy" || reason == "integrity" || reason == "unavailable"
}

func validateWithheld(s Source) error {
	w := s.Withheld
	if !s.Optional || s.Role != "artifact" || s.SourceID != "" || s.Path != "" || len(s.Associations) != 0 || s.Object != (Object{}) {
		return errors.New("withheld source must be a redacted optional artifact")
	}
	if !validDigest(w.SourceIDHash) || !validDigest(w.SnapshotCommitment) || !validWithheldReason(w.Reason) {
		return errors.New("invalid withheld source commitment or reason")
	}
	return nil
}

// WithholdSource redacts an optional selected artifact, preserving its source epoch
// and snapshot commitment. It never copies private descriptor fields into the result.
// The caller must also mark manifest enrichment partial with MissingSourceWithheld.
func WithholdSource(s Source, reason string) (Source, error) {
	if !validWithheldReason(reason) {
		return Source{}, errors.New("invalid withheld source reason")
	}
	if s.Withheld != nil {
		if err := validateSource(s, Identity{}); err != nil {
			return Source{}, err
		}
		w := *s.Withheld
		w.Reason = reason
		s.Withheld = &w
		return s, nil
	}
	// Validate against the session namespace encoded in the descriptor itself. The
	// enclosing manifest separately binds that namespace to its actual identity.
	if !s.Optional || s.Role != "artifact" || s.SourceID == "" || s.EpochID == "" || s.State == "" || !validOrder(s.Revision) || s.ObservedAtNS < 0 || s.CapturedAtNS < 0 ||
		!validStrings(s.SourceID, s.EpochID, s.State, s.Path) || !validStrings(s.Associations...) {
		return Source{}, errors.New("invalid optional artifact")
	}
	if err := validateObjectDescriptor(s.Object); err != nil {
		return Source{}, err
	}
	s.Withheld = &WithheldSource{SourceIDHash: SourceKeyHash(s), SnapshotCommitment: ObjectCommitment(s.Object), Reason: reason}
	s.SourceID, s.Path, s.Associations, s.Object = "", "", nil, Object{}
	return s, nil
}
