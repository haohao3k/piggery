package opencodeext

import (
	"regexp"
	"testing"
)

// The copy is what the entry needs and nothing else: every module and ../pi/<file> the plugin
// imports is in the tree, and every file in the tree is imported.
func TestTreeHasWhatTheEntryImports(t *testing.T) {
	tree, err := Tree()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path, b := range tree {
		if len(path) < 9 || path[:9] != "opencode/" {
			continue
		}
		for _, m := range regexp.MustCompile(`(?:"|')\.(\.?)/([A-Za-z0-9_-]+/)?([A-Za-z0-9_.-]+)(?:"|')`).FindAllStringSubmatch(string(b), -1) {
			target := "opencode/" + m[3]
			if m[1] == "." {
				target = m[2] + m[3] // ../pi/<file>
			}
			seen[target] = true
			if _, ok := tree[target]; !ok {
				t.Errorf("%s imports %s, which the copy does not carry", path, target)
			}
		}
	}
	for path := range tree {
		if path != Entry && !seen[path] {
			t.Errorf("%s is in the copy and nothing imports it", path)
		}
	}
}
