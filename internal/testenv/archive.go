package testenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
)

// Stream is a physical immutable record namespace, not an object or mutable head.
func Stream(machine, kind, agent, key string) string {
	return "v3/machines/" + machine + "/records/" + kind + "/" + raw.SHA256([]byte(agent+"\x00"+key)) + "/"
}

func FileStream(agent, key string) string { return Stream(MachineID, "file", agent, key) }

// Record is decoded independently of the collector's publication implementation.
type Record struct {
	Schema       int             `json:"schema"`
	MachineID    string          `json:"machine_id"`
	Kind         string          `json:"kind"`
	Agent        string          `json:"agent"`
	Key          string          `json:"key"`
	Sequence     uint64          `json:"sequence"`
	Parent       string          `json:"parent,omitempty"`
	File         *raw.Generation `json:"file,omitempty"`
	Manifest     map[string]any  `json:"manifest,omitempty"`
	Launch       map[string]any  `json:"launch,omitempty"`
	Root         map[string]any  `json:"root,omitempty"`
	Dependencies []Dependency    `json:"dependencies"`
	ID           string          `json:"-"`
	Object       string          `json:"-"`
}

type Dependency struct {
	Object    string `json:"object"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	RawSHA256 string `json:"raw_sha256,omitempty"`
	RawSize   int64  `json:"raw_size,omitempty"`
}

// DecodeRecord checks the physical key, descriptor digest and canonical JSON before
// trusting the payload. Plans and commits carry exactly the same descriptor bytes.
func DecodeRecord(key string, body []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(body, &r); err != nil {
		return r, err
	}
	var generic any
	if err := jsonx.Decode(body, &generic); err != nil {
		return r, err
	}
	canonical, err := jsonx.Compact(generic)
	if err != nil {
		return r, err
	}
	r.ID, r.Object = raw.SHA256(body), key
	prefix := Stream(r.MachineID, r.Kind, r.Agent, r.Key)
	name := fmt.Sprintf("%020d-%s.json", r.Sequence, r.ID)
	if r.Schema != 3 || r.Sequence == 0 || !bytes.Equal(body, canonical) || key != prefix+"commits/"+name && key != prefix+"plans/"+name {
		return r, fmt.Errorf("invalid physical descriptor %s", key)
	}
	count := 0
	for _, present := range []bool{r.File != nil, r.Manifest != nil, r.Launch != nil, r.Root != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return r, fmt.Errorf("%s has %d payloads", key, count)
	}
	// Metadata dependencies are a set. Check their real wire ordering before
	// any Golden normalization can remap path-derived hashes or sort that view.
	if r.Kind != "file" {
		for i := 1; i < len(r.Dependencies); i++ {
			if r.Dependencies[i-1].Object >= r.Dependencies[i].Object {
				return r, fmt.Errorf("%s metadata dependencies are unsorted or duplicated", key)
			}
		}
	}
	if r.File != nil {
		fields, ok := generic.(map[string]any)
		if !ok {
			return r, errors.New("invalid descriptor object")
		}
		if _, supplied := fields["dependencies"]; supplied && r.Dependencies == nil {
			r.Dependencies = []Dependency{}
		}
		g := r.File
		if err := g.Validate(r.MachineID); err != nil {
			return r, err
		}
		if g.Identity.Agent != r.Agent || g.Identity.Key != r.Key || g.Sequence != r.Sequence || g.Parent != r.Parent || r.Dependencies != nil && len(g.Pieces) != len(r.Dependencies) {
			return r, fmt.Errorf("%s inconsistent generation", key)
		}
		for i, p := range g.Pieces {
			if r.Dependencies == nil {
				continue
			}
			d := r.Dependencies[i]

			if d.Object != p.Object || d.SHA256 != p.EncodedSHA256 || d.Size != p.EncodedSize || d.RawSHA256 != p.SHA256 || d.RawSize != p.End-p.Start || d.Encoding != "zstd" {
				return r, fmt.Errorf("%s dependency differs from piece", key)
			}
		}
	}
	if r.File != nil && r.Dependencies == nil {
		for _, p := range r.File.Pieces {
			r.Dependencies = append(r.Dependencies, Dependency{Object: p.Object, SHA256: p.EncodedSHA256, Size: p.EncodedSize, Encoding: "zstd", RawSHA256: p.SHA256, RawSize: p.End - p.Start})
		}
	}
	return r, nil
}

// Chain selects only a unique complete parent chain, never a timestamp or list-order winner.
func Chain(keys []string, read func(string) ([]byte, error), prefix string) ([]Record, error) {
	return chain(keys, read, prefix, DecodeRecord)
}

func chain(keys []string, read func(string) ([]byte, error), prefix string, decode func(string, []byte) (Record, error)) ([]Record, error) {
	var commits []string
	for _, key := range keys {
		if strings.HasPrefix(key, prefix+"commits/") {
			commits = append(commits, key)
		}
	}
	slices.Sort(commits)
	var chain []Record
	parent := ""
	for i, key := range commits {
		body, err := read(key)
		if err != nil {
			return nil, err
		}
		r, err := decode(key, body)
		if err != nil {
			return nil, err
		}
		if r.Sequence != uint64(i+1) || r.Parent != parent {
			return nil, fmt.Errorf("divergent or incomplete chain %s", prefix)
		}
		plan, err := read(strings.Replace(key, "/commits/", "/plans/", 1))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(plan, body) {
			return nil, fmt.Errorf("%s plan/commit differ", key)
		}
		chain = append(chain, r)
		parent = r.ID
	}
	return chain, nil
}

// Pieces checks both encoded identity and decoded coverage; callers also check the full
// generation hash so valid individual pieces cannot hide reordering or omission.
func Pieces(pieces []raw.Piece, read func(string) ([]byte, error)) ([]byte, error) {
	if len(pieces) == 0 {
		return nil, errors.New("missing pieces")
	}
	var out []byte
	for _, p := range pieces {
		encoded, err := read(p.Object)
		if err != nil {
			return nil, err
		}
		if p.Object != raw.EncodedKey(p.EncodedSHA256) || raw.SHA256(encoded) != p.EncodedSHA256 || int64(len(encoded)) != p.EncodedSize {
			return nil, fmt.Errorf("%s encoded identity mismatch", p.Object)
		}
		data, err := raw.Decompress(encoded)
		if err != nil {
			return nil, err
		}
		if p.Start != int64(len(out)) || p.End-p.Start != int64(len(data)) || raw.SHA256(data) != p.SHA256 {
			return nil, fmt.Errorf("%s raw identity or coverage mismatch", p.Object)
		}
		out = append(out, data...)
	}
	return out, nil
}

func Content(g *raw.Generation, read func(string) ([]byte, error)) ([]byte, error) {
	data, err := Pieces(g.Pieces, read)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != g.Size || raw.SHA256(data) != g.SHA256 {
		return nil, fmt.Errorf("generation %d full content mismatch", g.Sequence)
	}
	return data, nil
}

func (e *Env) ReadObject(key string) ([]byte, error) {
	return os.ReadFile(filepath.Join(e.Bucket, filepath.FromSlash(key)))
}

func (e *Env) Records(prefix string) []Record {
	e.T.Helper()
	records, err := Chain(e.BucketKeys(), e.ReadObject, prefix)
	if err != nil {
		e.T.Fatal(err)
	}
	return records
}

func (e *Env) HasStream(prefix string) bool { return len(e.Records(prefix)) > 0 }

func (e *Env) History(agent, key string) []Record { return e.Records(FileStream(agent, key)) }

func (e *Env) GenerationContent(g *raw.Generation) []byte {
	e.T.Helper()
	data, err := Content(g, e.ReadObject)
	if err != nil {
		e.T.Fatal(err)
	}
	return data
}

func (e *Env) FileRecords(agent string) []Record {
	var out []Record
	for _, key := range e.BucketKeys() {
		if !strings.Contains(key, "/records/file/") || !strings.Contains(key, "/commits/") {
			continue
		}
		body, err := e.ReadObject(key)
		if err != nil {
			e.T.Fatal(err)
		}
		r, err := DecodeRecord(key, body)
		if err != nil {
			e.T.Fatal(err)
		}
		if r.Agent == agent {
			out = append(out, r)
		}
	}
	return out
}

// Audit is an independent physical-contract oracle. Every committed generation and
// dependency is readable, every chain is unique, and every blob is attributable to a
// discoverable plan (including an incomplete publication left by an interrupted writer).
// Archive caches expensive decoding only after proving immutable body identities.
// Audit rechecks every previously observed object's existence and SHA on every call;
// cached evidence therefore never excuses mutation, deletion or a newly broken chain.
type Archive struct {
	keys    []string
	read    func(string) ([]byte, error)
	hashes  map[string]string
	sizes   map[string]int64
	records map[string]Record
	content map[string][]byte
	decoded map[string][]byte
	checked map[string]bool
}

func NewArchive() *Archive {
	return &Archive{hashes: map[string]string{}, sizes: map[string]int64{}, records: map[string]Record{}, content: map[string][]byte{}, decoded: map[string][]byte{}, checked: map[string]bool{}}
}

func (a *Archive) decode(key string, body []byte) (Record, error) {
	if record, ok := a.records[key]; ok {
		return record, nil
	}
	record, err := DecodeRecord(key, body)
	if err != nil {
		return record, err
	}
	a.records[key] = record
	return record, nil
}

func (a *Archive) Chain(prefix string) ([]Record, error) {
	return chain(a.keys, a.read, prefix, a.decode)
}

func (a *Archive) Content(g *raw.Generation) ([]byte, error) {
	var signature strings.Builder
	fmt.Fprintf(&signature, "%s/%d", g.SHA256, g.Size)
	for _, p := range g.Pieces {
		fmt.Fprintf(&signature, "/%s/%d/%d/%s/%d/%s", p.EncodedSHA256, p.Start, p.End, p.Object, p.EncodedSize, p.SHA256)
	}
	id := signature.String()
	if body, ok := a.content[id]; ok {
		return body, nil
	}
	body, err := a.Pieces(g.Pieces)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != g.Size || raw.SHA256(body) != g.SHA256 {
		return nil, errors.New("generation full content mismatch")
	}
	a.content[id] = body
	return body, nil
}

func (a *Archive) Pieces(pieces []raw.Piece) ([]byte, error) {
	if len(pieces) == 0 {
		return nil, errors.New("missing pieces")
	}
	var out []byte
	for _, p := range pieces {
		if p.Object != raw.EncodedKey(p.EncodedSHA256) || a.hashes[p.Object] != p.EncodedSHA256 || a.sizes[p.Object] != p.EncodedSize {
			return nil, fmt.Errorf("%s encoded identity mismatch", p.Object)
		}
		data, ok := a.decoded[p.Object]
		if !ok {
			encoded, err := a.read(p.Object)
			if err != nil {
				return nil, err
			}
			if p.Object != raw.EncodedKey(p.EncodedSHA256) || raw.SHA256(encoded) != p.EncodedSHA256 || int64(len(encoded)) != p.EncodedSize {
				return nil, fmt.Errorf("%s encoded identity mismatch", p.Object)
			}
			data, err = raw.Decompress(encoded)
			if err != nil {
				return nil, err
			}
			a.decoded[p.Object] = data
		}
		if p.Start != int64(len(out)) || p.End-p.Start != int64(len(data)) || raw.SHA256(data) != p.SHA256 {
			return nil, fmt.Errorf("%s raw identity or coverage mismatch", p.Object)
		}
		out = append(out, data...)
	}
	return out, nil
}

func Audit(keys []string, read func(string) ([]byte, error)) error {
	return NewArchive().Audit(keys, read)
}

func (a *Archive) Audit(keys []string, read func(string) ([]byte, error)) error {
	present := map[string]bool{}
	for _, key := range keys {
		present[key] = true
		if !strings.HasPrefix(key, "v3/blobs/") && !strings.Contains(key, "/records/") && !strings.HasPrefix(key, "v3/snapshots/sets/") {
			continue
		}
		body, err := read(key)
		if err != nil {
			return err
		}
		sha := raw.SHA256(body)
		if old, ok := a.hashes[key]; ok && old != sha {
			return fmt.Errorf("immutable object changed: %s", key)
		}
		a.hashes[key] = sha
		a.sizes[key] = int64(len(body))
	}
	for key := range a.hashes {
		if !present[key] {
			return fmt.Errorf("immutable object disappeared: %s", key)
		}
	}
	a.keys, a.read = keys, read
	return a.audit(keys, read)
}

func (a *Archive) audit(keys []string, read func(string) ([]byte, error)) error {
	prefixes := map[string]bool{}
	referenced := map[string]bool{}
	for _, key := range keys {
		if strings.Contains(key, "/records/") {
			body, err := read(key)
			if err != nil {
				return err
			}
			r, err := a.decode(key, body)
			if err != nil {
				return err
			}
			prefix := Stream(r.MachineID, r.Kind, r.Agent, r.Key)
			committed := strings.HasPrefix(key, prefix+"commits/")
			if committed {
				prefixes[prefix] = true
			}
			for _, d := range r.Dependencies {
				referenced[d.Object] = true
				body, err := read(d.Object)
				if err != nil {
					if committed {
						return fmt.Errorf("%s: %w", key, err)
					}
					continue
				}
				if int64(len(body)) != d.Size || raw.SHA256(body) != d.SHA256 {
					return fmt.Errorf("%s dependency identity mismatch: %s", key, d.Object)
				}
				if d.Encoding == "zstd" {
					data, ok := a.decoded[d.Object]
					var err error
					if !ok {
						data, err = raw.Decompress(body)
						if err == nil {
							a.decoded[d.Object] = data
						}
					}
					if err != nil {
						return err
					}
					if raw.SHA256(data) != d.RawSHA256 || int64(len(data)) != d.RawSize {
						return fmt.Errorf("%s dependency raw identity mismatch", key)
					}
				}
			}
			if committed && r.Manifest != nil && !a.checked[key] {
				transcripts, _ := r.Manifest["transcripts"].([]any)
				for _, v := range transcripts {
					tr, ok := v.(map[string]any)
					if !ok {
						return fmt.Errorf("%s malformed transcript", key)
					}
					if err := a.checkStreamReference(r, tr, read); err != nil {
						return fmt.Errorf("%s: %w", key, err)
					}
				}
			}
			a.checked[key] = true
			if committed && r.File != nil {
				if _, err := a.Content(r.File); err != nil {
					return err
				}
			}
		}
		if strings.HasPrefix(key, "v3/snapshots/sets/") {
			body, err := read(key)
			if err != nil {
				return err
			}
			if key != "v3/snapshots/sets/"+raw.SHA256(body)+".json" {
				return fmt.Errorf("%s snapshot descriptor identity mismatch", key)
			}
		}
	}
	for prefix := range prefixes {
		if _, err := a.Chain(prefix); err != nil {
			return err
		}
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "v3/blobs/") && !referenced[key] {
			return fmt.Errorf("unattributed blob %s", key)
		}
	}
	return nil
}

// checkStreamReference checks a transcript that names its file stream: the manifest must
// depend on exactly that stream's first commit.
func (a *Archive) checkStreamReference(r Record, tr map[string]any, read func(string) ([]byte, error)) error {
	key, _ := tr["key"].(string)
	history := Stream(r.MachineID, "file", r.Agent, key) + "commits/"
	if tr["history"] != history || len(tr) != 3 {
		return fmt.Errorf("transcript %q is not a stream reference", key)
	}
	var firsts []string
	for _, d := range r.Dependencies {
		if strings.HasPrefix(d.Object, history+fmt.Sprintf("%020d-", 1)) {
			firsts = append(firsts, d.Object)
		}
	}
	if len(firsts) != 1 {
		return fmt.Errorf("transcript %q has %d first-commit dependencies", key, len(firsts))
	}
	body, err := read(firsts[0])
	if err != nil {
		return err
	}
	file, err := a.decode(firsts[0], body)
	if err != nil {
		return err
	}
	if file.File == nil || file.Key != key || file.Sequence != 1 {
		return fmt.Errorf("transcript %q first commit is another stream", key)
	}
	return nil
}
