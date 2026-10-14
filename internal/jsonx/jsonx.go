// Package jsonx decodes arbitrary JSON losslessly, and keeps an object's key order so a user's
// settings file is rewritten without reshuffling it.
package jsonx

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Decode parses data into v, keeping numbers as [json.Number] so large integers survive a round
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

func Map(data []byte) (m map[string]any, ok bool) {
	if err := Decode(data, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// Object holds nested objects as *Object.
type Object struct {
	keys   []string
	values map[string]any
}

func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

func (o *Object) Set(key string, v any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

func (o *Object) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

func (o *Object) Clone() *Object {
	c := NewObject()
	for _, k := range o.keys {
		c.Set(k, o.values[k])
	}
	return c
}

func (o *Object) Keys() []string {
	return slices.Clone(o.keys)
}

func (o *Object) Len() int {
	return len(o.keys)
}

func (o Object) MarshalJSON() ([]byte, error) {
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

func ParseObject(data []byte) (*Object, error) {
	// Settings must not silently lose duplicate members or invalid Unicode when edited or
	// checked for hook trust. The compatibility decoder alone accepts both.
	if !jsontext.Value(data).IsValid() {
		return nil, errors.New("json: invalid document or duplicate object names")
	}
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
	if err := consumeClosingDelim(dec); err != nil {
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
	if err := consumeClosingDelim(dec); err != nil {
		return nil, err
	}
	return out, nil
}

func consumeClosingDelim(dec *json.Decoder) error {
	_, err := dec.Token()
	return err
}

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

func Compact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

func SortedCompact(v any) ([]byte, error) {
	generic, err := toGeneric(v)
	if err != nil {
		return nil, err
	}
	return Compact(generic)
}

func SortedIndented(v any) ([]byte, error) {
	generic, err := toGeneric(v)
	if err != nil {
		return nil, err
	}
	out, err := Indent(generic, 1)
	if err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out, []byte{'\n'}), nil
}

// toGeneric turns struct fields into map keys, which encoding/json sorts.
func toGeneric(v any) (any, error) {
	body, err := Compact(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := Decode(body, &generic); err != nil {
		return nil, err
	}
	return generic, nil
}
