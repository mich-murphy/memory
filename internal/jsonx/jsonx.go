// Package jsonx holds JSON helpers: lossless decoding of arbitrary documents, and an object type
// that keeps its keys in order so a user's settings file is rewritten without reshuffling it.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Decode parses data into v, keeping numbers as json.Number so large integers survive a round
// trip.
func Decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("json: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("json: trailing data after the document")
	}
	return nil
}

// Map parses data as a JSON object; ok is false if it isn't one.
func Map(data []byte) (m map[string]any, ok bool) {
	if err := Decode(data, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// Object is a JSON object that remembers key order. Values are any JSON value, with nested
// objects as *Object.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

// Get returns the value for key.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

// Set adds or replaces key, keeping its position if it exists.
func (o *Object) Set(key string, v any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// Delete removes key.
func (o *Object) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// Clone returns a shallow copy.
func (o *Object) Clone() *Object {
	c := NewObject()
	for _, k := range o.keys {
		c.Set(k, o.values[k])
	}
	return c
}

// Keys returns the keys in order.
func (o *Object) Keys() []string {
	return slices.Clone(o.keys)
}

// Len is the number of keys.
func (o *Object) Len() int {
	return len(o.keys)
}

// MarshalJSON writes the object with its keys in order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := Compact(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := Compact(o.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ParseObject parses a JSON object, keeping key order at every level.
func ParseObject(data []byte) (*Object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("json: trailing data after the document")
	}
	obj, ok := v.(*Object)
	if !ok {
		return nil, errors.New("json: not an object")
	}
	return obj, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseObject(dec)
		case '[':
			return parseArray(dec)
		default:
			return nil, fmt.Errorf("unexpected %q", t)
		}
	default:
		return t, nil
	}
}

func parseObject(dec *json.Decoder) (*Object, error) {
	obj := NewObject()
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("object key %v is not a string", tok)
		}
		v, err := parseValue(dec)
		if err != nil {
			return nil, err
		}
		obj.Set(key, v)
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, err
	}
	return obj, nil
}

func parseArray(dec *json.Decoder) ([]any, error) {
	out := []any{}
	for dec.More() {
		v, err := parseValue(dec)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if _, err := dec.Token(); err != nil { // closing bracket
		return nil, err
	}
	return out, nil
}

// Indent marshals v indented with indent spaces per level and a trailing newline, the way
// Python's json.dumps(indent=n) lays out a settings file.
func Indent(v any, indent int) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", string(bytes.Repeat([]byte{' '}, indent)))
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return buf.Bytes(), nil
}

// Compact marshals v without HTML escaping.
func Compact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// Sorted marshals v with every object's keys sorted, struct fields included, the way Python's
// json.dumps(sort_keys=True) writes documents in the bucket. indent 0 means compact; otherwise
// it is the indent per level, and the output has no trailing newline.
func Sorted(v any, indent int) ([]byte, error) {
	body, err := Compact(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := Decode(body, &generic); err != nil {
		return nil, err
	}
	if indent == 0 {
		return Compact(generic)
	}
	out, err := Indent(generic, indent)
	if err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out, []byte{'\n'}), nil
}
