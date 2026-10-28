package jsonx

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestObjectKeepsKeyOrderAndDoesNotEscapeHTML(t *testing.T) {
	in := `{"z": 1, "a": {"y": [1, {"b": true}], "x": "a && b < c"}, "m": null}`
	obj, err := ParseObject([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	obj.Set("new", 1.5)
	obj.Set("z", 2)
	obj.Delete("m")
	obj.Delete("absent")
	got, err := Compact(obj)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"z":2,"a":{"y":[1,{"b":true}],"x":"a && b < c"},"new":1.5}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	c := obj.Clone()
	c.Set("z", 3)
	if v, _ := obj.Get("z"); v != 2 {
		t.Error("Clone shares values")
	}
	if obj.Len() != 3 {
		t.Errorf("Len = %d", obj.Len())
	}
}

func TestParseObjectRejects(t *testing.T) {
	for _, in := range []string{
		`[1]`, `{"a": 1} x`, `{"a": }`, `{1: 2}`, `{"a": [1,}`,
		`{"hooks":{},"hooks":{}}`, `{"hooks":{},"\u0068ooks":{}}`,
		`{"nested":{"command":"first","command":"second"}}`, `{"command":"\ud800"}`,
	} {
		if _, err := ParseObject([]byte(in)); err == nil {
			t.Errorf("ParseObject(%s) succeeded", in)
		}
	}
}

func TestSortedSortsStructFieldsToo(t *testing.T) {
	type s struct {
		B int `json:"b"`
		A int `json:"a"`
	}
	got, err := SortedCompact(map[string]any{"z": s{1, 2}, "a": []any{s{3, 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":[{"a":4,"b":3}],"z":{"a":2,"b":1}}`; string(got) != want {
		t.Errorf("got %s", got)
	}
	got, err = SortedIndented(map[string]any{"b": 1, "a": 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n \"a\": 2,\n \"b\": 1\n}"; string(got) != want {
		t.Errorf("got %q", got)
	}
}

func TestDecodeAndMap(t *testing.T) {
	var v any
	if err := Decode([]byte(`{"n": 12345678901234567890}`), &v); err != nil {
		t.Fatal(err)
	}
	if n := v.(map[string]any)["n"].(json.Number); n.String() != "12345678901234567890" {
		t.Errorf("number = %s", n)
	}
	if err := Decode([]byte(`{} {}`), &v); err == nil {
		t.Error("trailing data accepted")
	}
	if _, ok := Map([]byte(`[1]`)); ok {
		t.Error("Map accepted an array")
	}
	if m, ok := Map([]byte(`{"a": 1}`)); !ok || m["a"] == nil {
		t.Errorf("Map = %v, %v", m, ok)
	}
}

func FuzzParseObjectRoundTrip(f *testing.F) {
	f.Add(`{"a": [1, 2, {"b": null}], "c": "d"}`)
	f.Fuzz(func(t *testing.T, in string) {
		obj, err := ParseObject([]byte(in))
		if err != nil {
			return
		}
		out, err := Compact(obj)
		if err != nil {
			t.Fatal(err)
		}
		again, err := ParseObject(out)
		if err != nil {
			t.Fatalf("re-parse of %s: %v", out, err)
		}
		out2, err := Compact(again)
		if err != nil || !bytes.Equal(out2, out) {
			t.Fatalf("not stable: %s then %s", out, out2)
		}
	})
}
