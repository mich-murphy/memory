package publication

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// MarshalValidManifest must be ValidateManifest plus the exact json.Marshal bytes.
func TestMarshalValidManifestMatchesValidateManifest(t *testing.T) {
	_, valid, _ := fixture(t)
	oversize := valid
	oversize.Coverage.Missing = append(append([]string(nil), valid.Coverage.Missing...), strings.Repeat("x", MaxMetadata))
	cases := []struct {
		name    string
		m       Manifest
		wantErr string
	}{
		{"valid", valid, ""},
		{"manifest over limit", oversize, "metadata exceeds limit"},
	}
	for _, delta := range []int{-1, 0, 1} {
		_, m, _, _ := envelopeFixture(t, MaxMetadata+delta)
		wantErr := ""
		if delta > 0 {
			wantErr = "export metadata exceeds limit"
		}
		cases = append(cases, struct {
			name    string
			m       Manifest
			wantErr string
		}{fmt.Sprintf("envelope%+d", delta), m, wantErr})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantErr := ValidateManifest(tc.m)
			got, err := MarshalValidManifest(tc.m)
			if (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) {
				t.Fatalf("error %v, ValidateManifest %v", err, wantErr)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if want := mustJSON(t, tc.m); !bytes.Equal(got, want) {
					t.Fatal("bytes differ from json.Marshal")
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr || got != nil {
				t.Fatalf("got %q err %v, want error %q and no bytes", got, err, tc.wantErr)
			}
		})
	}
}
