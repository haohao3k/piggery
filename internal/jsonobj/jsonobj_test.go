package jsonobj

import (
	"encoding/json"
	"testing"
)

// An item added to an array and taken out again leaves the array's text as it was, whatever its
// layout (inline, one item per line), and the other items are never re-indented.
func TestArrayAppendRemoveKeepsLayout(t *testing.T) {
	for _, arr := range []string{
		`["a", ["b", {"c": 1}]]`,
		"[\n    \"a\",\n    [\"b\", {\"c\": 1}]\n  ]",
		`[]`,
	} {
		added, err := ArrayAppend(json.RawMessage(arr), []byte(`"new"`))
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(added) {
			t.Fatalf("%s -> %s", arr, added)
		}
		back, err := ArrayRemove(added, func(it json.RawMessage) bool { return string(it) == `"new"` })
		if err != nil || string(back) != arr {
			t.Errorf("%s -> %s -> %s (%v)", arr, added, back, err)
		}
	}
}
