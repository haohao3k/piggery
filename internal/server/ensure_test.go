package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

// EnsureFiles gives an old profile and a template the user wrote the keys they lack, keeping
// what they have (a comment too), and writes a profile that is missing; a second run changes nothing; a template its parser refuses is
// left as it is and named.
func TestEnsureFiles(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "harness"), 0o700)
	os.WriteFile(local.CodexProfilePath(dir), []byte("{\n  \"cmd\": \"codex\"\n}\n"), 0o600)
	mine := filepath.Join(manifests.Dir(dir), "mine", manifests.ManifestFile)
	os.MkdirAll(filepath.Dir(mine), 0o700)
	tmpl := "template: mine   # my own\nroles:\n  lead:\n    instructions: \"Lead.\"\n"
	os.WriteFile(mine, []byte(tmpl), 0o600)
	bad := filepath.Join(manifests.Dir(dir), "bad", manifests.ManifestFile)
	os.MkdirAll(filepath.Dir(bad), 0o700)
	os.WriteFile(bad, []byte("roles: {}\n"), 0o600)

	filled, errs := EnsureFiles(dir)
	if _, err := os.Stat(local.ProfilePath(dir)); err != nil {
		t.Fatalf("a missing profile was not written: %v", err)
	}
	got := map[string]string{}
	for _, f := range filled {
		got[f.Path] = strings.Join(f.Added, ",")
	}
	b, _ := os.ReadFile(mine)
	if !strings.Contains(got[local.CodexProfilePath(dir)], "blacklist") || !strings.Contains(got[mine], "roles.lead.spawn") ||
		!strings.HasPrefix(string(b), tmpl) || len(errs) != 1 || !strings.Contains(errs[0].Error(), "bad") {
		t.Fatalf("filled %v, errs %v, template:\n%s", got, errs, b)
	}
	if c, _ := os.ReadFile(local.CodexProfilePath(dir)); !strings.HasPrefix(string(c), "{\n  \"cmd\": \"codex\",\n  \"args\"") {
		t.Fatalf("a present profile lost its bytes:\n%s", c)
	}
	if again, errs := EnsureFiles(dir); len(again) != 0 || len(errs) != 1 {
		t.Fatalf("second run: %v %v", again, errs)
	}
}

// The shared prompt piggery ships: a first run writes its file and its config entry; once the user
// removes the entry, later runs leave it removed. Into a list the user has, the entry goes after
// the last item and every byte of the user's stays.
func TestEnsureFilesBuiltinPrompt(t *testing.T) {
	dir := t.TempDir()
	if _, errs := EnsureFiles(dir); len(errs) != 0 {
		t.Fatal(errs)
	}
	want := []PromptEntry{{File: "rules/general-policy.md", Roles: []string{"lead-peer/*", "slp/*"}}}
	set, err := LoadSettings(dir)
	if _, ferr := os.Stat(filepath.Join(dir, "rules", "general-policy.md")); err != nil || ferr != nil || !reflect.DeepEqual(set.Prompts, want) {
		t.Fatalf("first run: prompts %+v, %v, file %v", set.Prompts, err, ferr)
	}
	b, _ := os.ReadFile(ConfigPath(dir))
	os.WriteFile(ConfigPath(dir), []byte(strings.Replace(string(b), "\n  - file: rules/general-policy.md\n    roles: [\"lead-peer/*\", \"slp/*\"]", " []", 1)), 0o600)
	if set, _ := LoadSettings(dir); len(set.Prompts) != 0 {
		t.Fatalf("entry not removed: %+v", set.Prompts)
	}
	if filled, _ := EnsureFiles(dir); len(filled) != 0 {
		t.Fatalf("an entry the user removed came back: %v", filled)
	}

	mine := "prompts:\n  - file: rules/mine.md   # mine\n    roles: [\"*\"]\n\n# update\nupdate:\n  check: true\n"
	os.WriteFile(ConfigPath(dir), []byte(mine), 0o600)
	if added, err := AddPrompt(dir, want[0]); !added || err != nil {
		t.Fatalf("AddPrompt = %v, %v", added, err)
	}
	got, _ := os.ReadFile(ConfigPath(dir))
	if exp := strings.Replace(mine, "\n\n", "\n  - file: rules/general-policy.md\n    roles: [\"lead-peer/*\", \"slp/*\"]\n\n", 1); string(got) != exp {
		t.Fatalf("config:\n%s\nwant:\n%s", got, exp)
	}
}

// A role a later piggery offers the shared prompt goes into the user's entry once, from a home
// whose record predates the roles (it was offered lead-peer/* and slp/*): every other byte of
// config.yaml stays, and a role the user removes later stays removed. A role another entry for the
// file covers, an entry or a file the user removed get nothing, in a flow list or a block list alike.
func TestEnsureFilesNewRoleIntoUsersEntry(t *testing.T) {
	// a later piggery: it ships a template whose roles get the shared prompt too
	shipped := manifests.BuiltinPrompts
	manifests.BuiltinPrompts = []manifests.BuiltinPrompt{{File: shipped[0].File, Roles: append(slices.Clone(shipped[0].Roles), "next/*")}}
	t.Cleanup(func() { manifests.BuiltinPrompts = shipped })
	first := t.TempDir()
	if _, errs := EnsureFiles(first); len(errs) != 0 {
		t.Fatal(errs)
	}
	b, _ := os.ReadFile(ConfigPath(first))
	full := string(b) // piggery's own: every key, the entry with all three roles
	entry := "\n  - file: rules/general-policy.md\n    roles: [\"lead-peer/*\", \"slp/*\", \"next/*\"]"
	if !strings.Contains(full, entry) {
		t.Fatalf("setup: no entry in\n%s", full)
	}
	// oldHome: the file and the record as v0.9-v0.10 wrote them (no `roles:` key), the user's config.
	oldHome := func(cfg string) string {
		dir := t.TempDir()
		if _, errs := EnsureFiles(dir); len(errs) != 0 {
			t.Fatal(errs)
		}
		rp := filepath.Join(dir, "rules", ".builtin.json")
		var rec map[string]string
		raw, _ := os.ReadFile(rp)
		if err := json.Unmarshal(raw, &rec); err != nil || rec["roles:rules/general-policy.md"] == "" {
			t.Fatalf("record %s, %v", raw, err)
		}
		delete(rec, "roles:rules/general-policy.md")
		raw, _ = json.Marshal(rec)
		os.WriteFile(rp, raw, 0o600)
		os.WriteFile(ConfigPath(dir), []byte(cfg), 0o600)
		return dir
	}
	ensure := func(dir string) (filled []string, cfg string) {
		fs, errs := EnsureFiles(dir)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		for _, f := range fs {
			filled = append(filled, f.Added...)
		}
		c, _ := os.ReadFile(ConfigPath(dir))
		return filled, string(c)
	}

	// flow list: next/* goes in, a comment after the list stays; the user then removes it
	old := strings.Replace(full, `"slp/*", "next/*"]`, `"slp/*"]  # mine`, 1)
	dir := oldHome(old)
	filled, cfg := ensure(dir)
	if want := strings.Replace(full, `"next/*"]`, `"next/*"]  # mine`, 1); cfg != want || !reflect.DeepEqual(filled, []string{"prompts: rules/general-policy.md also for next/*"}) {
		t.Fatalf("filled %v, config:\n%s\nwant:\n%s", filled, cfg, want)
	}
	os.WriteFile(ConfigPath(dir), []byte(old), 0o600)
	if filled, cfg := ensure(dir); len(filled) != 0 || cfg != old {
		t.Fatalf("a role the user removed came back: %v\n%s", filled, cfg)
	}

	// block list: after the last item, written like it
	block := strings.Replace(full, `roles: ["lead-peer/*", "slp/*", "next/*"]`, "roles:\n      - lead-peer/*   # one\n      - slp/*", 1)
	filled, cfg = ensure(oldHome(block))
	if want := strings.Replace(block, "      - slp/*", "      - slp/*\n      - \"next/*\"", 1); cfg != want || len(filled) != 1 {
		t.Fatalf("block: filled %v, config:\n%s\nwant:\n%s", filled, cfg, want)
	}

	// an entry for the file before it already covers every role (`*`): nothing is added or said
	star := strings.Replace(old, "prompts:\n", "prompts:\n  - file: rules/general-policy.md\n    roles: [\"*\"]\n", 1)
	if star == old {
		t.Fatalf("setup: no prompts key in\n%s", old)
	}
	if filled, cfg := ensure(oldHome(star)); len(filled) != 0 || cfg != star {
		t.Fatalf("a role `*` covers: filled %v\n%s", filled, cfg)
	}

	// the user removed the file: it is not piggery's to offer to a role
	dir = oldHome(old)
	os.Remove(filepath.Join(dir, "rules", "general-policy.md"))
	if filled, cfg := ensure(dir); len(filled) != 0 || cfg != old {
		t.Fatalf("a file the user removed: filled %v\n%s", filled, cfg)
	}

	// the user removed the entry: nothing is added, nothing said
	gone := strings.Replace(full, entry, " []", 1)
	if filled, cfg := ensure(oldHome(gone)); len(filled) != 0 || cfg != gone {
		t.Fatalf("an entry the user removed: filled %v\n%s", filled, cfg)
	}
}
