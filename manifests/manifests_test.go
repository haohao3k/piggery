package manifests

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// Upgrading piggery: a template is one block. One exactly as Unpack wrote it is replaced whole by
// the new version; one the user changed in any way (a file edited, removed or added; a dot file does
// not count) is kept whole, its untouched files too, and Differing lists it. A template the user
// deleted is not unpacked again, a new built-in is added, and Unpack says which it wrote.
func TestUnpackUpgrade(t *testing.T) {
	home := t.TempDir()
	set := func(v string, names ...string) fstest.MapFS {
		fsys := fstest.MapFS{}
		for _, n := range names {
			fsys[n+".yaml"] = &fstest.MapFile{Data: []byte("template: " + n + "\nroles:\n  lead: {instructions_file: prompts/" + n + "/lead.md}\n  dev: {instructions_file: prompts/" + n + "/dev.md}\n")}
			fsys["prompts/"+n+"/lead.md"] = &fstest.MapFile{Data: []byte("lead " + v)}
			fsys["prompts/"+n+"/dev.md"] = &fstest.MapFile{Data: []byte("dev " + v)}
		}
		return fsys
	}
	names := []string{"calm", "dot", "edited", "added", "removed", "gone"}
	if got, err := unpack(home, set("v1", names...)); err != nil || len(got.Added) != len(names) || len(got.Updated) != 0 {
		t.Fatalf("first unpack: %+v, %v; want all %d added", got, err, len(names))
	}
	path := func(rel string) string { return filepath.Join(Dir(home), filepath.FromSlash(rel)) }
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(path(rel))
		if err != nil {
			return "(missing)"
		}
		return string(b)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(path("dot/.DS_Store"), []byte("x"), 0o600)) // not a change
	must(os.WriteFile(path("edited/prompts/edited/dev.md"), []byte("my dev"), 0o600))
	must(os.WriteFile(path("added/notes.md"), []byte("mine"), 0o600))
	must(os.Remove(path("removed/prompts/removed/dev.md")))
	must(os.RemoveAll(path("gone")))
	v2 := set("v2", append(names, "fresh")...)
	got, err := unpack(home, v2)
	if err != nil || !slices.Equal(got.Added, []string{"fresh"}) || !slices.Equal(got.Updated, []string{"calm", "dot"}) {
		t.Fatalf("unpack v2: %+v, %v; want fresh added, calm and dot updated", got, err)
	}
	for _, c := range []struct{ rel, want string }{
		{"calm/prompts/calm/lead.md", "lead v2"}, {"dot/prompts/dot/dev.md", "dev v2"}, {"fresh/prompts/fresh/lead.md", "lead v2"},
		{"edited/prompts/edited/dev.md", "my dev"}, {"edited/prompts/edited/lead.md", "lead v1"}, // the untouched file stays v1: no mix
		{"added/prompts/added/lead.md", "lead v1"}, {"removed/prompts/removed/lead.md", "lead v1"}, {"removed/prompts/removed/dev.md", "(missing)"},
		{"gone/manifest.yaml", "(missing)"},
	} {
		if got := read(c.rel); got != c.want {
			t.Errorf("%s = %q; want %q", c.rel, got, c.want)
		}
	}
	d, err := differing(home, v2)
	if err != nil || len(d) != 3 || d[0].Name != "added" || d[0].Diff[0] != "added notes.md" || d[1].Name != "edited" ||
		d[1].Diff[0] != "changed prompts/edited/dev.md" || d[2].Name != "removed" || d[2].Diff[0] != "removed prompts/removed/dev.md" {
		t.Fatalf("differing = %+v, %v; want added, edited and removed with how they differ", d, err)
	}
	// Resolve reads the home only, with the prompts inlined; nothing else is a template.
	if m, err := Resolve("edited", home); err != nil || !strings.Contains(m, "lead v1") || !strings.Contains(m, "my dev") || strings.Contains(m, "instructions_file") {
		t.Fatalf("resolve edited = %q, %v", m, err)
	}
	if _, err := Resolve("lead-peer", home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lead-peer (built into the binary, not in this home): %v; want not found", err)
	}
	// A template of the user's own with a built-in's name is listed as theirs and never written over.
	must(os.MkdirAll(path("late"), 0o700))
	must(os.WriteFile(path("late/manifest.yaml"), []byte("template: late\n"), 0o600))
	v3 := set("v3", append(names, "fresh", "late")...)
	if got, err := unpack(home, v3); err != nil || slices.Contains(got.Added, "late") || read("late/manifest.yaml") != "template: late\n" {
		t.Fatalf("unpack over a template of the user's own: %+v, %v", got, err)
	}
	if d, _ := differing(home, v3); !slices.ContainsFunc(d, func(u Updated) bool { return u.Name == "late" && u.Yours }) {
		t.Fatalf("differing = %+v; want late listed as the user's own", d)
	}
	// A block edited to exactly the version at hand is piggery's again: the next version replaces it.
	must(os.WriteFile(path("edited/manifest.yaml"), v3["edited.yaml"].Data, 0o600))
	must(os.WriteFile(path("edited/prompts/edited/lead.md"), []byte("lead v3"), 0o600))
	must(os.WriteFile(path("edited/prompts/edited/dev.md"), []byte("dev v3"), 0o600))
	if _, err := unpack(home, v3); err != nil {
		t.Fatal(err)
	}
	if got, err := unpack(home, set("v4", append(names, "fresh", "late")...)); err != nil || !slices.Contains(got.Updated, "edited") || read("edited/prompts/edited/dev.md") != "dev v4" {
		t.Fatalf("a block edited to the built-in then upgraded: %+v, %v, dev = %q", got, err, read("edited/prompts/edited/dev.md"))
	}
}

// template new copies the current built-in under a new name (its model renamed, nothing else
// changed) and never over an existing one.
func TestNew(t *testing.T) {
	home := t.TempDir()
	dir, err := New(home, "mine", "lead-peer")
	if err != nil {
		t.Fatal(err)
	}
	if m, err := Resolve("mine", home); err != nil || !strings.Contains(m, "instructions:") {
		t.Fatalf("new template %s: %v\n%s", dir, err, m)
	}
	// Only the model line changes (it names the team): the rest of the file, comments included.
	orig, _ := builtin.ReadFile("lead-peer.yaml")
	got, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
	if want := strings.Replace(string(orig), "template: lead-peer", "template: mine", 1); string(got) != want {
		t.Fatalf("copied manifest:\n%s\nwant:\n%s", got, want)
	}
	if _, err := New(home, "mine", "lead-peer"); err == nil {
		t.Fatal("new over an existing template: want refused")
	}
}

// A manifest that names itself with `model:` gets `template:` and no other byte changes; one that
// has `template:` is left alone; an unedited built-in so migrated is still tracked (the next
// Unpack updates it), an edited one still is not.
func TestMigrateKey(t *testing.T) {
	home := t.TempDir()
	v1 := fstest.MapFS{"sup.yaml": {Data: []byte("model: sup   # name\nroles: {a: {spawn: {model: small}}}\n")}}
	if _, err := unpack(home, v1); err != nil {
		t.Fatal(err)
	}
	mine := "# mine\nmodel: mine\nroles: {a: {}}\n"
	done := "template: done\nroles: {a: {}}\n"
	for name, text := range map[string]string{"mine": mine, "done": done} {
		if err := os.MkdirAll(filepath.Join(Dir(home), name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(Dir(home), name, ManifestFile), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := MigrateKey(home)
	if err != nil || len(changed) != 2 {
		t.Fatalf("changed %v, %v; want sup and mine", changed, err)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(Dir(home), name, ManifestFile))
		return string(b)
	}
	if got := read("sup"); got != "template: sup   # name\nroles: {a: {spawn: {model: small}}}\n" {
		t.Fatalf("sup: %q", got)
	}
	if got := read("mine"); got != "# mine\ntemplate: mine\nroles: {a: {}}\n" || read("done") != done {
		t.Fatalf("mine %q, done %q", got, read("done"))
	}
	if again, err := MigrateKey(home); err != nil || len(again) != 0 {
		t.Fatalf("second run changed %v, %v", again, err)
	}
	v2 := fstest.MapFS{"sup.yaml": {Data: []byte("template: sup\nsummary: v2\nroles: {a: {}}\n")}}
	if _, err := unpack(home, v2); err != nil {
		t.Fatal(err)
	}
	if got := read("sup"); !strings.Contains(got, "summary: v2") {
		t.Fatalf("a migrated built-in was not updated: %q", got)
	}
}

// A built-in a release drops is taken out of the home on the next unpack when the user never changed
// it: its files are deleted, then the directories, and it leaves .builtin.json. One the user changed
// stays whole, now their own template; a template that is still built in is untouched.
func TestUnpackDropsAReleasedBuiltin(t *testing.T) {
	home := t.TempDir()
	v1 := fstest.MapFS{
		"old.yaml":     {Data: []byte("template: old\nroles:\n  a: {instructions_file: prompts/a.md}\n  b: {instructions_file: prompts/b.md}\n")},
		"prompts/a.md": {Data: []byte("a")},
		"prompts/b.md": {Data: []byte("b")},
		"plain.yaml":   {Data: []byte("template: plain\n")},
		"stay.yaml":    {Data: []byte("template: stay\n")},
	}
	if _, err := unpack(home, v1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(home), "old/prompts/b.md"), []byte("my b"), 0o600); err != nil {
		t.Fatal(err)
	}
	v2 := fstest.MapFS{"stay.yaml": v1["stay.yaml"]} // old and plain are dropped
	if _, err := unpack(home, v2); err != nil {
		t.Fatal(err)
	}
	exists := func(rel string) bool { _, err := os.Stat(filepath.Join(Dir(home), rel)); return err == nil }
	if exists("plain") {
		t.Fatal("an unedited dropped built-in must be gone, directory included")
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(home), "old/prompts/b.md")); string(b) != "my b" || !exists("old/manifest.yaml") || !exists("old/prompts/a.md") || !exists("stay/manifest.yaml") {
		t.Fatalf("an edited dropped built-in stays whole (the user's now), and a current one is untouched: %q", b)
	}
	rec, _ := os.ReadFile(filepath.Join(Dir(home), recordFile))
	if strings.Contains(string(rec), `"old"`) || strings.Contains(string(rec), `"plain"`) || !strings.Contains(string(rec), `"stay"`) {
		t.Fatalf("record after the drop:\n%s", rec)
	}
	if _, err := unpack(home, v2); err != nil || !exists("old/prompts/b.md") { // and it stays the user's
		t.Fatalf("second unpack: %v", err)
	}
}

// Update on a copy that is piggery's own older version (the record says so) brings it to this
// version without --force and without a backup; the same file edited by the user is not touched.
func TestUpdateOlderOwnCopy(t *testing.T) {
	home := t.TempDir()
	if _, err := Unpack(home); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(Dir(home), "advisor", ManifestFile)
	want, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte("an older version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if u, err := Update(home, "advisor", false); err != nil || u.State != UpdateKept {
		t.Fatalf("a file the user changed: %+v, %v; want kept", u, err)
	}
	rec, _ := readRecord(Dir(home))
	rec["advisor"][ManifestFile] = hash([]byte("an older version\n")) // as if Unpack wrote that version
	if err := writeRecord(filepath.Join(Dir(home), recordFile), rec); err != nil {
		t.Fatal(err)
	}
	u, err := Update(home, "advisor", false)
	if got, _ := os.ReadFile(p); err != nil || u.State != UpdateUpdated || u.Backup != "" || string(got) != string(want) {
		t.Fatalf("an older own copy: %+v, %v; want updated in place, no backup", u, err)
	}
}

// silent_for becomes working_for in the timer rules of every template in the home, and no other byte
// changes (comments, spacing, other text with the word, a character wider than a byte before the key);
// a rule that has both keys, a quoted key, a manifest that does not parse and one without timers are
// left as they are; a second run changes nothing.
func TestMigrateTimers(t *testing.T) {
	home := t.TempDir()
	old := "# silent_for: the old name\ntemplate: t\nroles: {a: {}}\ntimers:\n" +
		"  - {on: a, notify: reports_to, silent_for: 20m}   # silent_for here\n" +
		"  - on: a\n    notify: reports_to\n    silent_for: 30m\n" +
		"  - {on: \"é\", notify: x, silent_for: 5m}\n" +
		"  - {on: a, notify: x, silent_for: 1m, working_for: 2m}\n" +
		"  - {on: a, notify: x, \"silent_for\": 1m}\n"
	want := "# silent_for: the old name\ntemplate: t\nroles: {a: {}}\ntimers:\n" +
		"  - {on: a, notify: reports_to, working_for: 20m}   # silent_for here\n" +
		"  - on: a\n    notify: reports_to\n    working_for: 30m\n" +
		"  - {on: \"é\", notify: x, working_for: 5m}\n" +
		"  - {on: a, notify: x, silent_for: 1m, working_for: 2m}\n" +
		"  - {on: a, notify: x, \"silent_for\": 1m}\n"
	files := map[string]string{
		"old":     old,
		"none":    "template: none\nroles: {a: {}}\n",
		"broken":  "template: [\ntimers:\n  - {silent_for: 1m}\n",
		"current": "template: current\ntimers:\n  - {on: a, notify: x, working_for: 1m}\n",
	}
	for name, text := range files {
		if err := os.MkdirAll(filepath.Join(Dir(home), name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(Dir(home), name, ManifestFile), []byte(text), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := MigrateTimers(home)
	if err != nil || len(changed) != 1 || filepath.Base(filepath.Dir(changed[0])) != "old" {
		t.Fatalf("changed %v, %v; want only old", changed, err)
	}
	for name, text := range files {
		got, _ := os.ReadFile(filepath.Join(Dir(home), name, ManifestFile))
		if exp := map[bool]string{true: want, false: text}[name == "old"]; string(got) != exp {
			t.Errorf("%s:\n got %q\nwant %q", name, got, exp)
		}
	}
	if st, _ := os.Stat(changed[0]); runtime.GOOS != "windows" && st.Mode().Perm() != 0o640 { // Windows has no mode bits
		t.Errorf("mode %v; want 0640 kept", st.Mode().Perm())
	}
	if again, err := MigrateTimers(home); err != nil || len(again) != 0 {
		t.Fatalf("second run changed %v, %v", again, err)
	}
}

// DropSpawnPins takes out the lines that set spawn.harness|model|thinking to a value and no other byte:
// inherit stays, so do allow_tools and every comment; a spawn left with nothing goes, or becomes
// `spawn: {}` when a comment sits inside it; flow style and a value on another line are refused.
func TestDropSpawnPins(t *testing.T) {
	old := "# t\ntemplate: t\nroles:\n" +
		"  a:\n    tools: [send]\n    spawn:        # how\n      model: small   # pin\n      thinking: max\n      harness: inherit\n      allow_tools: []\n" +
		"  b:\n    spawn:        # how\n      harness: codex\n    tools: [send]\n" +
		"  c:\n    spawn:\n      # kept for the Human\n      model: big\n    tools: [send]\r\n"
	want := "# t\ntemplate: t\nroles:\n" +
		"  a:\n    tools: [send]\n    spawn:        # how\n      harness: inherit\n      allow_tools: []\n" +
		"  b:\n    tools: [send]\n" +
		"  c:\n    spawn: {}\n      # kept for the Human\n    tools: [send]\r\n"
	got, err := DropSpawnPins([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	if pins, _ := SpawnPinsOf(got); len(pins) != 0 {
		t.Fatalf("pins left: %v", pins)
	}
	if again, err := DropSpawnPins(got); err != nil || string(again) != want {
		t.Fatalf("second run: %q, %v", again, err)
	}
	for name, text := range map[string]string{
		"flow spawn":  "template: t\nroles:\n  a:\n    spawn: {model: m, allow_tools: []}\n",
		"flow roles":  "template: t\nroles: {a: {spawn: {model: m}}}\n",
		"value below": "template: t\nroles:\n  a:\n    spawn:\n      model:\n        m\n",
	} {
		if out, err := DropSpawnPins([]byte(text)); err == nil {
			t.Errorf("%s: dropped to %q; want a refusal", name, out)
		}
	}
	if out, err := DropSpawnPins([]byte("template: t\nroles:\n  a:\n    spawn:\n      model: inherit\n")); err != nil || !strings.Contains(string(out), "model: inherit") {
		t.Errorf("nothing to drop: %q, %v", out, err)
	}
}
