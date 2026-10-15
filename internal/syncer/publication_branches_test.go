package syncer

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

func branchPublication(kind string) *publication {
	p := &publication{Schema: 3, MachineID: "machine", Kind: kind, Agent: "claude", Key: "session", Sequence: 1}
	switch kind {
	case "root":
		p.Root = &rootRecord{Agent: p.Agent, Path: "/root"}
		p.Key = streamID(p.Agent, p.Root.Path)
	case "launch":
		p.Key = "event"
		p.Launch = &launchRecord{Envelope: launch.Envelope{Schema: 1, ID: p.Key, Agent: p.Agent, Path: "/root/source", RecordedAt: 10, Payload: map[string]any{}}, Root: "/root", SessionKey: "session"}
	case "manifest":
		p.Manifest = &manifest{Schema: 3, Agent: p.Agent, MachineID: p.MachineID, SessionKey: p.Key, Info: map[string]any{}, FirstSeen: 10, SyncedAt: 20}
	case "file":
		p.Key = "source"
		body := []byte("transcript\n")
		encoded := raw.Compress(body)
		piece := raw.Piece{End: int64(len(body)), SHA256: raw.SHA256(body), EncodedSHA256: raw.SHA256(encoded), EncodedSize: int64(len(encoded)), Object: raw.EncodedKey(raw.SHA256(encoded))}
		p.File = &raw.Generation{Schema: 3, Identity: raw.Identity{Agent: p.Agent, MachineID: p.MachineID, Key: p.Key, SessionKey: "session", Root: "/root"}, Sequence: 1, Kind: "capture", Sources: []raw.Source{{Path: "/root/source", Encoding: "plain"}}, Role: string(adapters.RoleMain), Size: int64(len(body)), SHA256: piece.SHA256, LineCount: 1, Pieces: []raw.Piece{piece}, ObservedAt: 10}
		p.Dependencies = []dependency{pieceDependency(piece)}
	}
	return p
}

func branchSyncer(t *testing.T) (*Syncer, *memStore) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	bucket := newMemStore()
	return New(cfg, bucket, db, reviewAdapters(home), Options{}), bucket
}

func requireBranchError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("validation = %v, want error containing %q", err, want)
	}
}

func TestPublicationBranchesRejectInvalidTypedDescriptorsBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   string
		mutate func(*publication)
		want   string
	}{
		{"schema", "root", func(p *publication) { p.Schema = 2 }, "invalid publication identity"},
		{"machine", "root", func(p *publication) { p.MachineID = "other" }, "invalid publication identity"},
		{"agent", "root", func(p *publication) { p.Agent = "unknown" }, "invalid publication identity"},
		{"unsafeKey", "root", func(p *publication) { p.Key = "../escape" }, "invalid publication identity"},
		{"zeroSequence", "root", func(p *publication) { p.Sequence = 0 }, "invalid publication identity"},
		{"rootParent", "root", func(p *publication) { p.Parent = raw.SHA256([]byte("parent")) }, "invalid publication identity"},
		{"missingParent", "root", func(p *publication) { p.Sequence = 2 }, "invalid publication identity"},
		{"invalidParent", "root", func(p *publication) { p.Sequence = 2; p.Parent = "bad" }, "invalid publication identity"},
		{"noPayload", "root", func(p *publication) { p.Root = nil }, "exactly one payload"},
		{"twoPayloads", "root", func(p *publication) { p.Manifest = branchPublication("manifest").Manifest }, "exactly one payload"},
		{"unknownKind", "root", func(p *publication) { p.Kind = "unknown" }, "unknown publication kind"},
		{"missingFile", "root", func(p *publication) { p.Kind = "file" }, "missing file payload"},
		{"missingManifest", "root", func(p *publication) { p.Kind = "manifest" }, "invalid manifest payload"},
		{"missingLaunch", "root", func(p *publication) { p.Kind = "launch" }, "invalid launch payload"},
		{"missingRoot", "launch", func(p *publication) { p.Kind = "root" }, "invalid root payload"},
		{"rootSequence", "root", func(p *publication) { p.Sequence = 2; p.Parent = raw.SHA256([]byte("parent")) }, "invalid root payload"},
		{"rootAgent", "root", func(p *publication) { p.Root.Agent = "pi" }, "invalid root payload"},
		{"rootStream", "root", func(p *publication) { p.Key = "wrong-stream" }, "invalid root payload"},
		{"relativeRoot", "root", func(p *publication) { p.Root.Path = "relative"; p.Key = streamID(p.Agent, p.Root.Path) }, "invalid root payload"},
		{"launchSequence", "launch", func(p *publication) { p.Sequence = 2; p.Parent = raw.SHA256([]byte("parent")) }, "invalid launch payload"},
		{"launchAgent", "launch", func(p *publication) { p.Launch.Envelope.Agent = "pi" }, "invalid launch payload"},
		{"launchID", "launch", func(p *publication) { p.Launch.Envelope.ID = "other" }, "invalid launch payload"},
		{"launchSession", "launch", func(p *publication) { p.Launch.SessionKey = "../session" }, "invalid launch payload"},
		{"launchRoot", "launch", func(p *publication) { p.Launch.Root = "relative" }, "invalid launch payload"},
		{"launchTime", "launch", func(p *publication) { p.Launch.Snapshot.TakenAt = math.Inf(1) }, "invalid launch payload"},
		{"envelopeSchema", "launch", func(p *publication) { p.Launch.Envelope.Schema = 2 }, "launch: invalid envelope"},
		{"memoryWithoutHarness", "launch", func(p *publication) { id := raw.SHA256([]byte("memory")); p.Launch.Snapshot.MemoryID = &id }, "memory snapshot without harness"},
		{"manifestSchema", "manifest", func(p *publication) { p.Manifest.Schema = 2 }, "invalid manifest payload"},
		{"manifestAgent", "manifest", func(p *publication) { p.Manifest.Agent = "pi" }, "invalid manifest payload"},
		{"manifestMachine", "manifest", func(p *publication) { p.Manifest.MachineID = "other" }, "invalid manifest payload"},
		{"manifestSession", "manifest", func(p *publication) { p.Manifest.SessionKey = "other" }, "invalid manifest payload"},
		{"manifestInfo", "manifest", func(p *publication) { p.Manifest.Info = nil }, "invalid manifest payload"},
		{"manifestFirstSeen", "manifest", func(p *publication) { p.Manifest.FirstSeen = -1 }, "invalid manifest payload"},
		{"manifestSyncedAt", "manifest", func(p *publication) { p.Manifest.SyncedAt = math.NaN() }, "invalid manifest payload"},
		{"fileSchema", "file", func(p *publication) { p.File.Schema = 2 }, "raw generation: invalid identity"},
		{"pieceOffset", "file", func(p *publication) { p.File.Pieces[0].Start = 1 }, "invalid piece"},
		{"pieceKey", "file", func(p *publication) { p.File.Pieces[0].Object = "v3/blobs/zstd/wrong.zst" }, "invalid piece"},
		{"missingPieceDependency", "file", func(p *publication) { p.Dependencies = []dependency{} }, "dependencies do not cover pieces"},
		{"pieceDependencyHash", "file", func(p *publication) { p.Dependencies[0].RawSHA256 = raw.SHA256([]byte("other")) }, "file dependency mismatch"},
		{"fileAgent", "file", func(p *publication) { p.File.Identity.Agent = "pi" }, "file identity mismatch"},
		{"fileKey", "file", func(p *publication) { p.File.Identity.Key = "other" }, "file identity mismatch"},
		{"fileSequence", "file", func(p *publication) { p.File.Sequence = 2; p.File.Parent = raw.SHA256([]byte("parent")) }, "file identity mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sy, bucket := branchSyncer(t)
			p := branchPublication(tc.kind)
			if err := p.validate("machine"); err != nil {
				t.Fatalf("valid baseline: %v", err)
			}
			tc.mutate(p)
			requireBranchError(t, p.validate("machine"), tc.want)
			_, err := sy.publish(t.Context(), p, nil)
			requireBranchError(t, err, tc.want)
			ops, _ := bucket.takeOps()
			if len(ops) != 0 {
				t.Fatalf("invalid descriptor accessed bucket: %v", ops)
			}
			records, err := sy.db.ArchiveRecords(p.Kind, streamID(p.Agent, p.Key))
			if err != nil || len(records) != 0 {
				t.Fatalf("invalid descriptor cache = %+v, %v, want empty", records, err)
			}
		})
	}
}

func TestPublicationBranchesRejectInvalidDependencyDescriptors(t *testing.T) {
	encoded := raw.Compress([]byte("body"))
	valid := dependency{Object: raw.EncodedKey(raw.SHA256(encoded)), SHA256: raw.SHA256(encoded), Size: int64(len(encoded)), Encoding: "zstd", RawSHA256: raw.SHA256([]byte("body")), RawSize: 4}
	for _, tc := range []struct {
		name   string
		mutate func(*dependency)
		want   string
	}{
		{"unsafeKey", func(d *dependency) { d.Object = "v3/../escape" }, "invalid dependency"},
		{"legacyPrefix", func(d *dependency) { d.Object = "v2/blob" }, "invalid dependency"},
		{"hash", func(d *dependency) { d.SHA256 = "bad" }, "invalid dependency"},
		{"size", func(d *dependency) { d.Size = -1 }, "invalid dependency"},
		{"encoding", func(d *dependency) { d.Encoding = "gzip" }, "invalid dependency encoding"},
		{"rawHash", func(d *dependency) { d.RawSHA256 = "bad" }, "invalid compressed dependency"},
		{"rawSize", func(d *dependency) { d.RawSize = -1 }, "invalid compressed dependency"},
		{"noncanonicalKey", func(d *dependency) { d.Object = "v3/blobs/zstd/alias.zst" }, "invalid compressed dependency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sy, bucket := branchSyncer(t)
			p := branchPublication("root")
			p.Dependencies = []dependency{valid}
			if err := p.validate("machine"); err != nil {
				t.Fatalf("valid baseline: %v", err)
			}
			tc.mutate(&p.Dependencies[0])
			_, err := sy.publish(t.Context(), p, nil)
			requireBranchError(t, err, tc.want)
			ops, _ := bucket.takeOps()
			if len(ops) != 0 {
				t.Fatalf("invalid dependency accessed bucket: %v", ops)
			}
		})
	}
}

func branchReference(t *testing.T, sy *Syncer, bucket *memStore, file *publication) (*publication, []byte) {
	t.Helper()
	body, id, err := canonicalPublication(file)
	if err != nil {
		t.Fatal(err)
	}
	key := sy.publicationKey(file, true, id)
	if err := bucket.Put(t.Context(), key, body, map[string]string{"sha256": id}); err != nil {
		t.Fatal(err)
	}
	p := branchPublication("manifest")
	g := file.File
	p.Manifest.Transcripts = []transcript{{Key: file.Key, Role: adapters.Role(g.Role), Size: g.Size, SHA256: g.SHA256, LineCount: g.LineCount, Object: key, Commit: key, Pieces: g.Pieces, History: sy.publicationPrefix("file", file.Agent, file.Key) + "commits/"}}
	p.Dependencies = []dependency{{Object: key, SHA256: id, Size: int64(len(body)), Encoding: "plain"}}
	return p, body
}

func TestPublicationBranchesTranscriptReferencesRequireExactCommittedEvidence(t *testing.T) {
	for _, damage := range []string{"missingDependency", "planOnly", "wrongEncoding", "checksum", "size", "missingRemote", "keyIdentity", "invalidBody", "invalidPublication", "wrongSession", "wrongAgent", "wrongKey", "transcriptSize", "transcriptHash", "transcriptRole", "transcriptLines", "transcriptPieces", "transcriptHistory", "duplicateDependency", "corruptCache"} {
		t.Run(damage, func(t *testing.T) {
			sy, bucket := branchSyncer(t)
			file := branchPublication("file")
			p, body := branchReference(t, sy, bucket, file)
			if err := p.validate("machine"); err != nil {
				t.Fatalf("baseline descriptor: %v", err)
			}
			if err := sy.verifyReferences(t.Context(), p, nil); err != nil {
				t.Fatalf("baseline committed references: %v", err)
			}
			tr := &p.Manifest.Transcripts[0]
			want := ""
			switch damage {
			case "missingDependency":
				p.Dependencies = nil
				want = "missing committed metadata dependency"
			case "planOnly":
				tr.Commit = strings.Replace(tr.Commit, "/commits/", "/plans/", 1)
				tr.Object = tr.Commit
				p.Dependencies[0].Object = tr.Commit
				if err := bucket.Put(t.Context(), tr.Commit, body, nil); err != nil {
					t.Fatal(err)
				}
				want = "missing committed metadata dependency"
			case "wrongEncoding":
				p.Dependencies[0].Encoding = "zstd"
				want = "missing committed metadata dependency"
			case "checksum":
				p.Dependencies[0].SHA256 = raw.SHA256([]byte("other"))
				want = "metadata dependency checksum mismatch"
			case "size":
				p.Dependencies[0].Size++
				want = "metadata dependency checksum mismatch"
			case "missingRemote":
				if err := bucket.Delete(t.Context(), tr.Commit); err != nil {
					t.Fatal(err)
				}
				want = "not found"
			case "keyIdentity":
				key := sy.publicationPrefix("file", "claude", "different") + "commits/" + filepath.Base(tr.Commit)
				if err := bucket.Put(t.Context(), key, body, nil); err != nil {
					t.Fatal(err)
				}
				tr.Commit, tr.Object, p.Dependencies[0].Object = key, key, key
				want = "referenced publication identity mismatch"
			case "invalidBody", "invalidPublication":
				if damage == "invalidBody" {
					body = []byte(`{"unknown":true}`)
					want = "unknown field"
				} else {
					file.Schema = 2
					var err error
					body, _, err = canonicalPublication(file)
					if err != nil {
						t.Fatal(err)
					}
					want = "invalid publication identity"
				}
				id := raw.SHA256(body)
				key := sy.publicationKey(file, true, id)
				if err := bucket.Put(t.Context(), key, body, nil); err != nil {
					t.Fatal(err)
				}
				tr.Commit, tr.Object = key, key
				p.Dependencies[0] = dependency{Object: key, SHA256: id, Size: int64(len(body)), Encoding: "plain"}
			case "wrongSession", "wrongAgent", "wrongKey":
				switch damage {
				case "wrongSession":
					file.File.Identity.SessionKey = "other"
				case "wrongAgent":
					file.Agent = "pi"
					file.File.Identity.Agent = "pi"
				case "wrongKey":
					file.Key = "other"
					file.File.Identity.Key = "other"
				}
				changed, _ := branchReference(t, sy, bucket, file)
				tr.Commit, tr.Object = changed.Manifest.Transcripts[0].Commit, changed.Manifest.Transcripts[0].Object
				p.Dependencies = changed.Dependencies
				want = "transcript identity mismatch"
			case "transcriptSize":
				tr.Size++
				want = "transcript content differs"
			case "transcriptHash":
				tr.SHA256 = raw.SHA256([]byte("other"))
				want = "transcript content differs"
			case "transcriptRole":
				tr.Role = adapters.RoleAux
				want = "transcript content differs"
			case "transcriptLines":
				tr.LineCount++
				want = "transcript content differs"
			case "transcriptPieces":
				tr.Pieces = append([]raw.Piece(nil), tr.Pieces...)
				tr.Pieces[0].End++
				want = "transcript content differs"
			case "transcriptHistory":
				tr.History = "v3/wrong/commits/"
				want = "transcript content differs"
			case "duplicateDependency":
				p.Dependencies = append(p.Dependencies, p.Dependencies[0])
				want = "duplicate dependency identity"
			case "corruptCache":
				if err := sy.db.ImportPlan(state.Record{Key: tr.Commit, Kind: "file", Body: []byte("corrupt cached record")}); err != nil {
					t.Fatal(err)
				}
				want = "metadata dependency checksum mismatch"
			}
			requireBranchError(t, sy.verifyReferences(t.Context(), p, nil), want)
			// Mutations of an otherwise valid graph must fail before even a plan is written.
			if damage != "wrongEncoding" && damage != "duplicateDependency" {
				if err := p.validate("machine"); err != nil {
					t.Fatalf("mutated descriptor is structurally invalid: %v", err)
				}
				bucket.takeOps()
				_, err := sy.publish(t.Context(), p, nil)
				requireBranchError(t, err, want)
				ops, _ := bucket.takeOps()
				for _, op := range ops {
					if strings.HasPrefix(op, "put ") {
						t.Fatalf("rejected reference wrote remote metadata: %v", ops)
					}
				}
			}
			sess, err := sy.db.Session(adapters.SessionRef{Agent: p.Agent, Key: p.Key})
			if err != nil || sess != nil {
				t.Fatalf("rejected reference projected session = %+v, %v", sess, err)
			}
		})
	}
}

func TestPublicationBranchesManifestMembersRejectInconsistentHistory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*manifest)
		want   string
	}{
		{"duplicateTranscript", func(m *manifest) { m.Transcripts = append(m.Transcripts, m.Transcripts[0]) }, "invalid transcript reference"},
		{"transcriptObject", func(m *manifest) { m.Transcripts[0].Object = "v3/other" }, "invalid transcript reference"},
		{"deletionUnknown", func(m *manifest) { m.DeletedLocally[0].Key = "other" }, "invalid deletion reference"},
		{"deletionTime", func(m *manifest) { m.DeletedLocally[0].At = 0 }, "invalid deletion reference"},
		{"snapshotID", func(m *manifest) { m.Snapshots[0].ID = "invalid" }, "invalid snapshot reference"},
		{"memoryID", func(m *manifest) { id := "invalid"; m.Snapshots[0].MemoryID = &id }, "invalid snapshot reference"},
		{"snapshotTiming", func(m *manifest) { m.Snapshots[0].Timing = "pre_session" }, "unsupported snapshot timing"},
		{"snapshotCheckedAt", func(m *manifest) { m.Snapshots[0].CheckedAt = math.Inf(1) }, "invalid snapshot reference"},
		{"snapshotMtime", func(m *manifest) { m.Snapshots[0].Mtimes["instructions"] = -1 }, "invalid snapshot mtime"},
		{"launchRoot", func(m *manifest) { m.Launches[0].Root = "relative" }, "invalid launch identity"},
		{"launchEnvelope", func(m *manifest) { m.Launches[0].Envelope.Payload = nil }, "launch: invalid envelope"},
		{"launchSnapshot", func(m *manifest) { m.Launches[0].Snapshot.ID = "invalid" }, "invalid snapshot reference"},
		{"launchSession", func(m *manifest) { m.Launches[0].SessionKey = "other" }, "launch session mismatch"},
		{"missingFirstLaunch", func(m *manifest) { m.Launch = nil }, "inconsistent first launch"},
		{"missingLaunchHistory", func(m *manifest) { m.Launches = nil }, "inconsistent first launch"},
		{"firstLaunchDiffers", func(m *manifest) { m.Launch.Root = "/different" }, "first launch differs from history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sy, bucket := branchSyncer(t)
			p, _ := branchReference(t, sy, bucket, branchPublication("file"))
			snap := state.SnapshotEntry{ID: raw.SHA256([]byte("snapshot")), Timing: "captured", TakenAt: 10, CheckedAt: 20, Mtimes: map[string]float64{"instructions": 5}}
			entry := *branchPublication("launch").Launch
			entry.Snapshot = snap
			first := entry
			p.Manifest.Launch, p.Manifest.Launches = &first, []launchRecord{entry}
			p.Manifest.Snapshots = []state.SnapshotEntry{snap}
			p.Manifest.DeletedLocally = []deletion{{Key: p.Manifest.Transcripts[0].Key, At: 30}}
			if err := p.validate("machine"); err != nil {
				t.Fatalf("valid baseline: %v", err)
			}
			tc.mutate(p.Manifest)
			bucket.takeOps()
			_, err := sy.publish(t.Context(), p, nil)
			requireBranchError(t, err, tc.want)
			ops, _ := bucket.takeOps()
			if len(ops) != 0 {
				t.Fatalf("invalid history accessed bucket: %v", ops)
			}
		})
	}
}

func TestPublicationBranchesDependencyVerificationRejectsAlteredBytesAndMetadata(t *testing.T) {
	for _, encoding := range []string{"plain", "zstd"} {
		for _, damage := range []string{"size", "encodedHash", "rawHash", "rawSize", "missingMetadata", "wrongMetadata", "rawMetadata"} {
			if encoding == "plain" && (damage == "rawHash" || damage == "rawSize" || damage == "rawMetadata") {
				continue
			}
			t.Run(encoding+"/"+damage, func(t *testing.T) {
				bucket := newMemStore()
				rawBody := []byte("archived bytes")
				body := rawBody
				if encoding == "zstd" {
					body = raw.Compress(rawBody)
				}
				d := dependency{Object: "v3/test-dependency", Encoding: encoding, Size: int64(len(body)), SHA256: raw.SHA256(body), RawSize: int64(len(rawBody)), RawSHA256: raw.SHA256(rawBody)}
				meta := map[string]string{"sha256": d.SHA256}
				if encoding == "zstd" {
					meta["encoded_sha256"], meta["sha256"] = d.SHA256, d.RawSHA256
				}
				if err := bucket.Put(t.Context(), d.Object, body, meta); err != nil {
					t.Fatal(err)
				}
				if err := verifyDependency(t.Context(), bucket, d); err != nil {
					t.Fatalf("valid bytes: %v", err)
				}
				if err := verifyDependencyMetadata(t.Context(), bucket, d); err != nil {
					t.Fatalf("valid metadata: %v", err)
				}
				switch damage {
				case "size":
					d.Size++
					requireBranchError(t, verifyDependency(t.Context(), bucket, d), "encoded size mismatch")
					requireBranchError(t, verifyDependencyMetadata(t.Context(), bucket, d), "size mismatch")
				case "encodedHash":
					d.SHA256 = raw.SHA256([]byte("different"))
					requireBranchError(t, verifyDependency(t.Context(), bucket, d), "encoded checksum mismatch")
				case "rawHash":
					d.RawSHA256 = raw.SHA256([]byte("different"))
					requireBranchError(t, verifyDependency(t.Context(), bucket, d), "raw checksum or size mismatch")
				case "rawSize":
					d.RawSize--
					requireBranchError(t, verifyDependency(t.Context(), bucket, d), "raw checksum or size mismatch")
				case "missingMetadata", "wrongMetadata", "rawMetadata":
					if damage == "missingMetadata" {
						meta = nil
					}
					if damage == "wrongMetadata" {
						meta["content_sha256"] = raw.SHA256([]byte("other"))
					}
					if damage == "rawMetadata" {
						meta["sha256"] = raw.SHA256([]byte("other"))
					}
					if err := bucket.Put(t.Context(), d.Object, body, meta); err != nil {
						t.Fatal(err)
					}
					want := "checksum identity mismatch"
					if damage == "rawMetadata" {
						want = "raw checksum identity mismatch"
					}
					requireBranchError(t, verifyDependencyMetadata(t.Context(), bucket, d), want)
				}
			})
		}
	}
}

func TestPublicationBranchesDecodeRejectsTrailingAndUnknownData(t *testing.T) {
	body, _, err := canonicalPublication(branchPublication("root"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePublication(body); err != nil {
		t.Fatalf("valid baseline: %v", err)
	}
	for _, suffix := range []string{"{}", "not-json"} {
		if _, err := decodePublication(append(append([]byte(nil), body...), suffix...)); err == nil || !strings.Contains(err.Error(), "trailing data") {
			t.Fatalf("decodePublication suffix %q = %v, want trailing data rejection", suffix, err)
		}
	}
	body = append([]byte(`{"unknown":true,`), body[1:]...)
	_, err = decodePublication(body)
	requireBranchError(t, err, "unknown field")
}
