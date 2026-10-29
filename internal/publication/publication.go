package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

func validIdentity(i Identity) bool {
	return i.ArchiveID != "" && i.MachineID != "" && i.Agent != "" && i.SessionID != "" &&
		validStrings(i.ArchiveID, i.MachineID, i.Agent, i.SessionID)
}

// Ordering coordinates fit the language-neutral consumer's signed BIGINT range.
func validOrder(value uint64) bool { return value > 0 && value <= math.MaxInt64 }

func validStrings(values ...string) bool {
	for _, value := range values {
		if len(value) > MaxMetadata || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return false
		}
	}
	return true
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}

func decode(b []byte, v any) error {
	if !utf8.Valid(b) {
		return errors.New("invalid metadata UTF-8")
	}
	if len(b) > MaxMetadata {
		return errors.New("metadata exceeds limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid metadata: %w", err)
	}
	var tail any
	if err := d.Decode(&tail); !errors.Is(err, io.EOF) {
		return errors.New("trailing metadata")
	}
	return nil
}

func readMetadata(ctx context.Context, b Backend, key string) ([]byte, error) {
	f, err := b.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, r: f}, MaxMetadata+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, err
	}
	if len(data) > MaxMetadata {
		return nil, invalidMetadata{errors.New("metadata exceeds limit")}
	}
	return data, nil
}

func archiveValid(a Archive) error {
	if a.Contract != Contract {
		return errors.New("unsupported archive contract")
	}
	if a.ArchiveID == "" || !validStrings(a.ArchiveID) {
		return errors.New("invalid archive identity")
	}
	return nil
}

func ReadArchive(ctx context.Context, b Backend) (Archive, error) {
	var a Archive
	data, err := readMetadata(ctx, b, ArchiveKey)
	if err != nil {
		return a, err
	}
	if err := decode(data, &a); err != nil {
		return a, invalidMetadata{err}
	}
	if err := archiveValid(a); err != nil {
		return a, invalidMetadata{err}
	}
	return a, nil
}

func WriteArchive(ctx context.Context, b Backend, a Archive) error {
	if err := archiveValid(a); err != nil {
		return err
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	// Archive binding is create-only, including recovery from an ambiguous ack.
	err = b.CompareAndSwap(ctx, ArchiveKey, "", data)
	if err == nil || errors.Is(err, ErrDurability) {
		return err
	}
	got, e := ReadArchive(ctx, b)
	if e == nil && got == a {
		return fence(ctx, b, ArchiveKey)
	}
	return err
}

func fence(ctx context.Context, b Backend, key string) error {
	if durable, ok := b.(DurabilityFence); ok {
		return durable.Sync(ctx, key)
	}
	return nil
}

func fencePublication(ctx context.Context, b Backend, m Manifest, c Current) error {
	for _, key := range []string{m.Identity.CurrentKey(), c.ManifestKey, ArchiveKey} {
		if err := fence(ctx, b, key); err != nil {
			return err
		}
	}
	for n := range m.Sources {
		if !IsSelected(m.Sources[n]) {
			continue
		}
		if err := fence(ctx, b, m.Sources[n].Object.Key); err != nil {
			return dependencyError(m.Sources[n], "object-fence", err)
		}
	}
	return nil
}

func checkArchive(ctx context.Context, b Backend, i Identity) error {
	if !validIdentity(i) {
		return errors.New("invalid session identity")
	}
	a, err := ReadArchive(ctx, b)
	if err != nil {
		return err
	}
	if a.ArchiveID != i.ArchiveID {
		return errors.New("archive identity mismatch")
	}
	return nil
}

// ValidateManifest checks the complete wire shape without accessing backend dependencies.
func ValidateManifest(m Manifest) error {
	if m.Contract != Contract {
		return errors.New("unsupported manifest contract")
	}
	if !validIdentity(m.Identity) || m.PublicationID == "" || !validStrings(m.PublicationID) || !validOrder(m.Sequence) {
		return errors.New("invalid manifest identity or sequence")
	}
	if m.ObservedAtNS < 0 || m.CapturedAtNS < 0 || m.PublishedAtNS < 0 {
		return errors.New("negative manifest timestamp")
	}
	if len(m.Sources) > MaxSources {
		return errors.New("source limit exceeded")
	}
	if m.Coverage.Discovery == "" || m.Coverage.Enrichment == "" ||
		!validStrings(m.Coverage.Discovery, m.Coverage.Enrichment) || !validStrings(m.Coverage.Missing...) {
		return errors.New("coverage required")
	}
	seen := map[[2]string]bool{}
	for n := range m.Sources {
		s := &m.Sources[n]
		k := [2]string{SourceKeyHash(*s), s.EpochID}
		if seen[k] {
			return errors.New("invalid or duplicate source epoch")
		}
		if err := validateSource(*s, m.Identity); err != nil {
			return err
		}
		if !IsSelected(*s) && (m.Coverage.Enrichment != "partial" || !slices.Contains(m.Coverage.Missing, MissingSourceWithheld)) {
			return errors.New("withheld source requires partial enrichment and missing marker")
		}
		seen[k] = true
	}
	data, err := json.Marshal(m)
	if err != nil || len(data) > MaxMetadata {
		return errors.New("metadata exceeds limit")
	}
	// A durable publication must fit the consumer envelope too: selected source
	// and epoch identities are repeated in the export's exact body table.
	_, _, err = exportMetadata(m)
	return err
}

// validateSource checks one source's provenance and selected object. Uniqueness
// belongs to the enclosing manifest, not an individual source.
func validateSource(s Source, i Identity) error {
	if s.EpochID == "" || s.Role == "" || s.State == "" || !validOrder(s.Revision) ||
		!validStrings(s.SourceID, s.EpochID, s.Role, s.Path, s.State) || !validStrings(s.Associations...) {
		return errors.New("invalid or duplicate source epoch")
	}
	if s.ObservedAtNS < 0 || s.CapturedAtNS < 0 {
		return errors.New("invalid source size or timestamp")
	}
	if s.Optional && s.Role != "artifact" {
		return errors.New("only artifacts may be optional")
	}
	if s.Withheld != nil {
		return validateWithheld(s)
	}
	if s.SourceID == "" {
		return errors.New("invalid or duplicate source epoch")
	}
	return validateObject(s.Object, i)
}

// validateObject checks the bounded streaming coordinates and exact session-local
// content address; dependency existence and byte verification happen on read.
func validateObject(o Object, i Identity) error {
	if err := validateObjectDescriptor(o); err != nil {
		return err
	}
	if o.Key != i.BaseKey()+"/objects/"+o.EncodedSHA256+".zst" {
		return errors.New("object key outside session content namespace")
	}
	return nil
}

func validateObjectDescriptor(o Object) error {
	if o.Key == "" || !validStrings(o.Key, o.Encoding) {
		return errors.New("invalid object descriptor")
	}
	if o.Size < 0 || o.EncodedSize < 0 || o.Size == math.MaxInt64 || o.EncodedSize == math.MaxInt64 {
		return errors.New("invalid source size or timestamp")
	}
	if o.Encoding != "zstd" || !validDigest(o.SHA256) || !validDigest(o.EncodedSHA256) {
		return errors.New("unsupported encoding or invalid digest")
	}
	return nil
}

func parseCurrent(data []byte, i Identity) (Current, error) {
	var c Current
	if err := decode(data, &c); err != nil {
		return c, err
	}
	if c.Contract != Contract {
		return c, errors.New("unsupported current contract")
	}
	if !validIdentity(i) || c.Identity != i || c.PublicationID == "" || !validStrings(c.PublicationID) || !validOrder(c.Sequence) || !validDigest(c.ManifestSHA256) {
		return c, errors.New("invalid current identity")
	}
	if c.ManifestKey != i.BaseKey()+"/manifests/"+c.ManifestSHA256+".json" {
		return c, errors.New("invalid manifest key")
	}
	return c, nil
}

func resolve(ctx context.Context, b Backend, i Identity, c Current) (Manifest, error) {
	var m Manifest
	data, err := readMetadata(ctx, b, c.ManifestKey)
	if err != nil {
		return m, err
	}
	if token(data) != c.ManifestSHA256 {
		return m, errors.New("manifest checksum mismatch")
	}
	if err := decode(data, &m); err != nil {
		return m, err
	}
	if err := ValidateManifest(m); err != nil {
		return m, err
	}
	if m.Identity != i || m.PublicationID != c.PublicationID || m.Sequence != c.Sequence {
		return m, errors.New("manifest/current mismatch")
	}
	return m, nil
}

func pinned(ctx context.Context, b Backend, i Identity) (Manifest, error) {
	if err := checkArchive(ctx, b, i); err != nil {
		return Manifest{}, err
	}
	data, _, err := b.Current(ctx, i.CurrentKey())
	if err != nil {
		return Manifest{}, err
	}
	c, err := parseCurrent(data, i)
	if err != nil {
		return Manifest{}, err
	}
	return resolve(ctx, b, i, c)
}

// streamObject verifies both representations, reading at most declared sizes+1.
// Decoder memory/window is capped at 64 MiB; body size is not a memory allocation.
func streamObject(ctx context.Context, b Backend, o Object, dst io.Writer) (retErr error) {
	f, err := b.Open(ctx, o.Key)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	encodedHash := sha256.New()
	encoded := &io.LimitedReader{R: io.TeeReader(&contextReader{ctx: ctx, r: f}, encodedHash), N: o.EncodedSize + 1}
	dec, err := zstd.NewReader(encoded, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecoderMaxWindow(64<<20))
	if err != nil {
		return err
	}
	defer dec.Close()
	decodedHash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, decodedHash), io.LimitReader(dec, o.Size+1))
	if err != nil {
		return fmt.Errorf("decode source: %w", err)
	}
	if n != o.Size {
		return errors.New("decoded size mismatch")
	}
	// Consume remaining encoded bytes so trailing garbage/extra frames cannot hide.
	if _, err = io.Copy(io.Discard, encoded); err != nil {
		return err
	}
	if o.EncodedSize+1-encoded.N != o.EncodedSize {
		return errors.New("encoded size mismatch")
	}
	if hex.EncodeToString(encodedHash.Sum(nil)) != o.EncodedSHA256 || hex.EncodeToString(decodedHash.Sum(nil)) != o.SHA256 {
		return errors.New("source checksum mismatch")
	}
	return nil
}

// verify streams the complete selected closure; Read always uses it.
func verify(ctx context.Context, b Backend, m Manifest) error {
	return verifyExcept(ctx, b, m, nil)
}

// verifyExcept streams every selected object except those in trusted.
func verifyExcept(ctx context.Context, b Backend, m Manifest, trusted map[Object]bool) error {
	for n := range m.Sources {
		s := &m.Sources[n]
		if !IsSelected(*s) || trusted[s.Object] {
			continue
		}
		if err := streamObject(ctx, b, s.Object, io.Discard); err != nil {
			return dependencyError(*s, "verify", err)
		}
	}
	return nil
}

// Read resolves one current root and verifies its complete dependency closure.
func Read(ctx context.Context, b Backend, i Identity) (Manifest, error) {
	m, err := pinned(ctx, b, i)
	if err != nil {
		return m, err
	}
	return m, verify(ctx, b, m)
}

func writeMetadata(ctx context.Context, b Backend, key string, data []byte) error {
	f, err := os.CreateTemp("", "publication-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // Best-effort temporary cleanup.
	_, err = f.Write(data)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return b.PutFile(ctx, key, f.Name())
}

func advances(old, next Manifest) error {
	if next.Sequence <= old.Sequence || next.PublicationID == old.PublicationID {
		return ErrConflict
	}
	sources := map[[2]string]Source{}
	for n := range next.Sources {
		s := &next.Sources[n]
		sources[[2]string{SourceKeyHash(*s), s.EpochID}] = *s
	}
	for n := range old.Sources {
		s := &old.Sources[n]
		n, ok := sources[[2]string{SourceKeyHash(*s), s.EpochID}]
		if !ok || n.Revision < s.Revision || (n.Revision == s.Revision && sourceCommitment(n) != sourceCommitment(*s)) || (!s.Optional && n.Optional) {
			return fmt.Errorf("%w: source epoch regression", ErrConflict)
		}
	}
	return nil
}

// trustedFrom returns the selected objects of next that are byte-for-byte the
// same descriptor (key, encoding, digests, sizes) as a selected object of prev.
//
// Invariant relied on: an object referenced by a published manifest stays present
// and unmodified until the reviewed cutover deletion of contract section 6. Objects
// are immutable and content addressed (create-only PutFile), the contract says to
// "begin without automatic GC" and that future GC "must honor current/retained
// roots" (section 4), and section 6 step 6 permits deletion only of "the reviewed
// retired destination/key set after acceptance". prev is the remote current's
// manifest, which Publish fully verified (or, inductively, trusted from a fully
// verified predecessor) when it was published. Read never uses this shortcut.
func trustedFrom(prev, next Manifest) map[Object]bool {
	have := map[Object]bool{}
	for n := range prev.Sources {
		if IsSelected(prev.Sources[n]) {
			have[prev.Sources[n].Object] = true
		}
	}
	out := map[Object]bool{}
	for n := range next.Sources {
		if o := next.Sources[n].Object; IsSelected(next.Sources[n]) && have[o] {
			out[o] = true
		}
	}
	return out
}

// TrustedObjects lists the selected objects of m already present in the manifest
// of the remote current that m would advance. Any failure to read, validate or
// advance from that manifest yields nil, i.e. the full upload/verify path.
func TrustedObjects(ctx context.Context, b Backend, m Manifest) map[Object]bool {
	data, _, err := b.Current(ctx, m.Identity.CurrentKey())
	if err != nil {
		return nil
	}
	c, err := parseCurrent(data, m.Identity)
	if err != nil || c.PublicationID == m.PublicationID {
		return nil
	}
	prev, err := resolve(ctx, b, m.Identity, c)
	if err != nil || advances(prev, m) != nil {
		return nil
	}
	return trustedFrom(prev, m)
}

func publicationEligible(ctx context.Context, b Backend, m Manifest, c Current, expectedToken string) (bool, map[Object]bool, error) {
	var trusted map[Object]bool
	oldData, got, err := b.Current(ctx, m.Identity.CurrentKey())
	switch {
	case errors.Is(err, ErrNotFound):
		got = ""
	case err != nil:
		return false, nil, err
	default:
		old, err := parseCurrent(oldData, m.Identity)
		if err != nil {
			return false, nil, err
		}
		if old == c {
			_, err := resolve(ctx, b, m.Identity, old)
			return true, nil, err
		}
		prev, err := resolve(ctx, b, m.Identity, old)
		if err != nil {
			return false, nil, err
		}
		if err := advances(prev, m); err != nil {
			return false, nil, err
		}
		trusted = trustedFrom(prev, m)
	}
	if got != expectedToken {
		return false, nil, ErrConflict
	}
	return false, trusted, nil
}

// Publish uploads a complete immutable manifest after verifying dependencies, then
// guards current with the caller's token. Retry the identical manifest after an
// ambiguous acknowledgement; a newer selected publication is never overwritten.
func Publish(ctx context.Context, b Backend, m Manifest, expectedToken string) (Current, error) {
	var zero Current
	if err := ValidateManifest(m); err != nil {
		return zero, err
	}
	if err := checkArchive(ctx, b, m.Identity); err != nil {
		return zero, err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return zero, err
	}
	if len(data) > MaxMetadata {
		return zero, errors.New("metadata exceeds limit")
	}
	digest := token(data)
	c := Current{Contract: Contract, Identity: m.Identity, PublicationID: m.PublicationID, Sequence: m.Sequence, ManifestSHA256: digest, ManifestKey: m.Identity.BaseKey() + "/manifests/" + digest + ".json"}
	already, trusted, err := publicationEligible(ctx, b, m, c, expectedToken)
	if err != nil {
		return zero, err
	}
	if already {
		if err := verify(ctx, b, m); err != nil {
			return zero, err
		}
		return c, fencePublication(ctx, b, m, c)
	}
	if err := verifyExcept(ctx, b, m, trusted); err != nil {
		return zero, err
	}
	if err := writeMetadata(ctx, b, c.ManifestKey, data); err != nil {
		return zero, err
	}
	currentData, err := json.Marshal(c)
	if err != nil {
		return zero, err
	}
	err = b.CompareAndSwap(ctx, m.Identity.CurrentKey(), expectedToken, currentData)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, ErrDurability) {
		return zero, err
	}
	// Read-back only accepts the exact selected identity+manifest; never blindly
	// repeat a conditional write after a negative read of an in-flight request.
	ack, _, readErr := b.Current(ctx, m.Identity.CurrentKey())
	if readErr == nil {
		selected, e := parseCurrent(ack, m.Identity)
		if e == nil && selected == c {
			return c, fencePublication(ctx, b, m, c)
		}
	}
	return zero, err
}
