package raw

import "testing"

func validGeneration() Generation {
	sha := SHA256(nil)
	encoded := Compress(nil)
	encodedSHA := SHA256(encoded)
	return Generation{Schema: 3, Identity: Identity{Agent: "claude", MachineID: "machine", Key: "file", Root: "/root"}, Sequence: 1, Kind: "capture", Sources: []Source{{Path: "/file", Encoding: "plain"}}, Size: 0, SHA256: sha, Pieces: []Piece{{SHA256: sha, EncodedSHA256: encodedSHA, EncodedSize: int64(len(encoded)), Object: EncodedKey(encodedSHA)}}}
}

func TestGenerationValidatesEmptyAndParentCoverage(t *testing.T) {
	g := validGeneration()
	if err := g.Validate("machine"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Generation)
	}{
		{"schema", func(g *Generation) { g.Schema = 2 }},
		{"machine", func(g *Generation) { g.Identity.MachineID = "other" }},
		{"escape", func(g *Generation) { g.Identity.Key = "../escape" }},
		{"parent", func(g *Generation) { g.Parent = SHA256([]byte("parent")) }},
		{"kind", func(g *Generation) { g.Kind = "unknown" }},
		{"delete", func(g *Generation) { g.Kind = "delete" }},
		{"sources", func(g *Generation) { g.Sources = nil }},
		{"coverage", func(g *Generation) { g.Size = 1 }},
		{"object", func(g *Generation) { g.Pieces[0].Object = "wrong" }},
		{"offset", func(g *Generation) { g.Pieces[0].Start = 1 }},
		{"encodedSize", func(g *Generation) { g.Pieces[0].EncodedSize = 0 }},
		{"emptyHash", func(g *Generation) { g.SHA256 = SHA256([]byte("not empty")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := validGeneration()
			tc.change(&g)
			if err := g.Validate("machine"); err == nil {
				t.Fatal("Validate accepted corrupt descriptor")
			}
		})
	}
	g.Sequence = 2
	g.Parent = SHA256([]byte("parent"))
	g.Kind = "delete"
	g.DeletedAt = 1
	if err := g.Validate("machine"); err != nil {
		t.Fatal(err)
	}
}
