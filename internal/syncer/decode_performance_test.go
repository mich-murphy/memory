package syncer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// referenceDecodePublication is the original decoder, retained independently to
// check wire compatibility rather than relying on canonical writer output alone.
func referenceDecodePublication(body []byte) (*publication, error) {
	var p publication
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, fmt.Errorf("archive record: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("archive record: trailing data")
	}
	// An explicitly supplied legacy file dependency list must cover the pieces, even
	// when encoded as null or an empty array. Absence alone selects derivation.
	if p.File != nil && p.Dependencies == nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, err
		}
		if _, present := fields["dependencies"]; present {
			p.Dependencies = []dependency{}
		}
	}
	return &p, nil
}

func TestDecodePublicationReferenceEquivalence(t *testing.T) {
	for _, kind := range []string{"file", "session", "manifest", "launch", "root"} {
		p := branchPublication(kind)
		if kind == "manifest" {
			p.Manifest = branchPublication("session").Manifest
			p.Manifest.Schema = copiedManifestSchema
		}
		if err := p.validate("machine"); err != nil {
			t.Fatalf("%s fixture is not valid: %v", kind, err)
		}
		body, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "dependencies")
		base, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		prefix := strings.TrimSuffix(string(base), "}")
		list, err := json.Marshal(p.Dependencies)
		if err != nil {
			t.Fatal(err)
		}
		cases := map[string]string{
			"canonical": string(body), "absent": string(base),
			"trailing-value": string(body) + " null", "trailing-garbage": string(body) + " !",
			"invalid-syntax":                  string(body[:len(body)-1]),
			"unknown-top":                     prefix + `,"unexpected":true}`,
			"numeric-max":                     prefix + `,"sequence":18446744073709551615,"dependencies":[{"size":9223372036854775807,"raw_size":-9223372036854775808}]}`,
			"numeric-overflow":                prefix + `,"sequence":18446744073709551616}`,
			"numeric-negative":                prefix + `,"sequence":-1}`,
			"numeric-fraction":                prefix + `,"sequence":1.5}`,
			"number-info":                     prefix + `,"manifest":{"info":{"number":18446744073709551615}}}`,
			"duplicate-list-null":             prefix + `,"dependencies":[{"size":3}],"dependencies":null}`,
			"duplicate-list-list":             prefix + `,"dependencies":[{"size":3}],"dependencies":[{"object":"second"}]}`,
			"duplicate-null-case":             prefix + `,"dependencies":null,"Dependencies":null}`,
			"duplicate-list-case-null":        prefix + `,"Dependencies":[{"size":3}],"dependencies":null}`,
			"duplicate-bad-good":              prefix + `,"dependencies":false,"dependencies":[]}`,
			"duplicate-nested-unknown":        prefix + `,"dependencies":[{"unknown":0}],"dependencies":[]}`,
			"dependency-error-before-unknown": `{"dependencies":false,"unknown":true}`,
			"dependency-error-before-type":    `{"dependencies":false,"sequence":"bad"}`,
			"unknown-before-dependency-error": `{"unknown":true,"dependencies":false}`,
			"bad-payload":                     prefix + `,"file":{"unexpected":true}}`,
			"duplicate-file":                  prefix + `,"file":null}`,
		}
		values := []string{"null", "[]", string(list), `[null]`, `[{"size":9223372036854775807}]`, `[{"size":-9223372036854775808}]`, `[{"size":9223372036854775808}]`, `[{"size":-9223372036854775809}]`, `[{"size":1.5}]`, `[{"size":"bad"}]`, `[{"unexpected":true}]`, `false`, `{}`, `"bad"`, `1`}
		for _, key := range []string{`dependencies`, `Dependencies`, `DEPENDENCIES`, `depen\u0064encies`} {
			for i, value := range values {
				cases[fmt.Sprintf("%s-%d", key, i)] = prefix + `,"` + key + `":` + value + `}`
			}
		}
		for name, body := range cases {
			t.Run(kind+"/"+name, func(t *testing.T) { comparePublicationDecoders(t, []byte(body)) })
		}
	}
	for _, body := range []string{"null", "[]", "true", "", "{", `{"dependencies":[}`, `{"dependencies":null} {}`, `{"dependencies":null} x`} {
		t.Run("syntax/"+body, func(t *testing.T) { comparePublicationDecoders(t, []byte(body)) })
	}
}

func comparePublicationDecoders(t *testing.T, body []byte) {
	t.Helper()
	want, wantErr := referenceDecodePublication(body)
	got, gotErr := decodePublication(body)
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("decode error = %v, want %v", gotErr, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded publication differs from original: got %#v, want %#v", got, want)
	}
	if gotErr == nil && fmt.Sprint(got.validate("machine")) != fmt.Sprint(want.validate("machine")) {
		t.Fatal("validation differs from original")
	}
}
