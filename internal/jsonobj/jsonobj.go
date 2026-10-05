// Package jsonobj edits a JSON object and keeps what it does not touch: member order and the raw
// bytes of every value read from the file. Members are written in the two-space layout (the one pi,
// Codex and piggery write); a value set by the caller is indented to match.
package jsonobj

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Object is a JSON object whose members keep their order and raw values, so a settings file piggery
// edits keeps everything it does not touch. Written back with two-space indentation.
type Object []Member

type Member struct {
	Key string
	Val json.RawMessage
	raw bool // Val was read by Parse and is written as it is, not re-indented
}

func Parse(b []byte) (Object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return Object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var o Object
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, Member{key, v, true})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o Object) Get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == key {
			return m.Val, true
		}
	}
	return nil, false
}

// Set replaces key's value in place, or appends it.
func (o Object) Set(key string, v json.RawMessage) Object {
	for i, m := range o {
		if m.Key == key {
			o[i].Val, o[i].raw = v, false
			return o
		}
	}
	return append(o, Member{Key: key, Val: v})
}

func (o Object) Del(key string) Object {
	var out Object
	for _, m := range o {
		if m.Key != key {
			out = append(out, m)
		}
	}
	return out
}

// Bytes renders o at indent depth depth (0 = a file: ends with a newline).
func (o Object) Bytes(depth int) []byte {
	pad := bytes.Repeat([]byte("  "), depth)
	if len(o) == 0 {
		return []byte("{}")
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, m := range o {
		b.Write(pad)
		b.WriteString("  ")
		b.Write(String(m.Key))
		b.WriteString(": ")
		var v bytes.Buffer
		if m.raw || json.Indent(&v, m.Val, string(pad)+"  ", "  ") != nil {
			v.Reset()
			v.Write(m.Val)
		}
		b.Write(v.Bytes())
		if i < len(o)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.Write(pad)
	b.WriteByte('}')
	if depth == 0 {
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// String encodes s without HTML escaping (as JSON.stringify and serde write it).
func String(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// SetVerbatim is Set for a value that is written exactly as it is (an edited copy of a value read
// from the file), not indented.
func (o Object) SetVerbatim(key string, v json.RawMessage) Object {
	o = o.Set(key, v)
	for i := range o {
		if o[i].Key == key {
			o[i].raw = true
		}
	}
	return o
}

// itemSpans is the [start, end) of each item of the JSON array arr.
func itemSpans(arr json.RawMessage) ([][2]int, error) {
	dec := json.NewDecoder(bytes.NewReader(arr))
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return nil, errors.New("not a JSON array")
	}
	var spans [][2]int
	for dec.More() {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		end := int(dec.InputOffset())
		spans = append(spans, [2]int{end - len(v), end})
	}
	return spans, nil
}

// ArrayAppend adds item (a JSON value) at the end of the array arr and keeps the rest of its text,
// layout included: after the last item on its line (", item") for an array on one line, else on a
// new line indented like the last item.
func ArrayAppend(arr json.RawMessage, item []byte) (json.RawMessage, error) {
	spans, err := itemSpans(arr)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return append(append([]byte("["), item...), ']'), nil
	}
	last := spans[len(spans)-1]
	sep := []byte(", ")
	if bytes.IndexByte(arr, '\n') >= 0 {
		ls := bytes.LastIndexByte(arr[:last[0]], '\n') + 1
		sep = append([]byte(",\n"), arr[ls:last[0]]...)
	}
	out := append([]byte(nil), arr[:last[1]]...)
	out = append(append(out, sep...), item...)
	return append(out, arr[last[1]:]...), nil
}

// ArrayRemove drops the items of arr for which drop is true, with the comma and space that
// separated them, and keeps the rest of the text.
func ArrayRemove(arr json.RawMessage, drop func(item json.RawMessage) bool) (json.RawMessage, error) {
	spans, err := itemSpans(arr)
	if err != nil {
		return nil, err
	}
	out := append(json.RawMessage(nil), arr...)
	for i := len(spans) - 1; i >= 0; i-- { // from the end, so earlier spans stay valid
		s := spans[i]
		if !drop(arr[s[0]:s[1]]) {
			continue
		}
		switch {
		case i+1 < len(spans):
			out = append(out[:s[0]], out[spans[i+1][0]:]...)
		case i > 0:
			out = append(out[:spans[i-1][1]], out[s[1]:]...)
		default:
			out = append(out[:s[0]], out[s[1]:]...)
		}
	}
	return out, nil
}
