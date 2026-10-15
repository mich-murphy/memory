package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLiveFileDependenciesSupportDerivedAndStrictLegacy(t *testing.T) {
	piece := `{"object":"v3/blobs/zstd/encoded.zst","encoded_sha256":"encoded","encoded_size":30,"sha256":"raw","start":0,"end":100}`
	dependency := `{"object":"v3/blobs/zstd/encoded.zst","sha256":"encoded","size":30,"encoding":"zstd","raw_sha256":"raw","raw_size":100}`
	for _, scenario := range []struct {
		name, suffix string
		valid        bool
	}{
		{"derived", "", true},
		{"legacy", `,"dependencies":[` + dependency + `]`, true},
		{"null", `,"dependencies":null`, false},
		{"empty", `,"dependencies":[]`, false},
		{"extra", `,"dependencies":[` + dependency + `,` + dependency + `]`, false},
		{"checksum", `,"dependencies":[` + strings.Replace(dependency, `"sha256":"encoded"`, `"sha256":"corrupt"`, 1) + `]`, false},
		{"size", `,"dependencies":[` + strings.Replace(dependency, `"size":30`, `"size":31`, 1) + `]`, false},
		{"encoding", `,"dependencies":[` + strings.Replace(dependency, `"encoding":"zstd"`, `"encoding":"plain"`, 1) + `]`, false},
		{"rawChecksum", `,"dependencies":[` + strings.Replace(dependency, `"raw_sha256":"raw"`, `"raw_sha256":"corrupt"`, 1) + `]`, false},
		{"rawSize", `,"dependencies":[` + strings.Replace(dependency, `"raw_size":100`, `"raw_size":99`, 1) + `]`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var p livePublication
			if err := json.Unmarshal([]byte(`{"file":{"pieces":[`+piece+`]}`+scenario.suffix+`}`), &p); err != nil {
				t.Fatal(err)
			}
			if err := liveValidateFileDependencies(p); (err == nil) != scenario.valid {
				t.Fatalf("dependency validation = %v, valid=%t", err, scenario.valid)
			}
		})
	}
}
