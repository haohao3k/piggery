package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sting8k/piggery/internal/server"
	"github.com/sting8k/piggery/manifests"
)

// The upgrade's second step through the real commands: a plain setup lists what the user changed (a
// built-in template, the shared general-policy prompt) and every template's deprecated keys, and
// replaces nothing; setup --outdated replaces what --replace names (a bad name stops it before it
// touches anything), what the user ticks, and nothing when it cannot ask; a replaced one is
// the built-in again with the user's copy in a .backup.
func TestSetupOutdatedTemplates(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	if _, err := manifests.Unpack(dir); err != nil {
		t.Fatal(err)
	}
	if _, errs := server.EnsureFiles(dir); len(errs) != 0 {
		t.Fatal(errs)
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := Main(dir, args, &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, out := run("template", "new", "mine", "--from", "slp"); code != 0 {
		t.Fatal(out)
	}
	mine := filepath.Join(dir, "templates", "mine", manifests.ManifestFile)
	b, _ := os.ReadFile(mine)
	text := strings.ReplaceAll(string(b), "working_for", "silent_for")
	spawn := regexp.MustCompile(`(?m)^    spawn:[^\n]*\n`)
	loc := spawn.FindStringIndex(text)
	text = text[:loc[1]] + "      model: small\n" + text[loc[1]:] // a model choice in the manifest, the old way
	if err := os.WriteFile(mine, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	lead := filepath.Join(dir, "templates", "amp-like", "prompts", "amp-like", "lead.md")
	policy := filepath.Join(dir, "rules", "general-policy.md")
	for _, f := range []string{lead, policy} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, append(b, "my own line\n"...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(f string) string { b, _ := os.ReadFile(f); return string(b) }
	leadEdited, policyEdited := read(lead), read(policy)
	leadKept, policyKept := func() bool { return read(lead) == leadEdited }, func() bool { return read(policy) == policyEdited }

	_, out := run("setup")
	for _, want := range []string{
		"! amp-like: you changed prompts/amp-like/lead.md, so it stays as yours; `piggery template update amp-like --force`",
		"! general-policy: you changed rules/general-policy.md, so it stays as yours; `piggery setup --outdated --replace general-policy`",
		"✓ silent_for renamed working_for in mine",
		"✓ model choices moved to config.yaml roles: mine (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("setup does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "silent_for is") || strings.Contains(out, "use silent_for") || strings.Contains(out, "deprecated") {
		t.Errorf("setup warns about silent_for, which it renamed:\n%s", out)
	}
	if b, _ := os.ReadFile(mine); strings.Contains(string(b), "silent_for") {
		t.Errorf("silent_for is still in the template:\n%s", b)
	}
	if _, out := run("setup"); strings.Contains(out, "renamed") {
		t.Errorf("said again:\n%s", out)
	}
	if b, _ := os.ReadFile(server.ConfigPath(dir)); !strings.Contains(string(b), `"mine/`) || strings.Contains(read(mine), "model: small") {
		t.Errorf("the model choice did not move: config.yaml\n%s\nmanifest\n%s", b, read(mine))
	}
	if _, out := run("check"); strings.Contains(out, "spawn.") {
		t.Errorf("check still warns about spawn:\n%s", out)
	}
	if !leadKept() || !policyKept() {
		t.Fatal("a plain setup replaced something the user changed")
	}

	code, out := run("setup", "--outdated")
	if code != 0 || !strings.Contains(out, "kept as yours: amp-like, general-policy") || !strings.Contains(out, "--replace amp-like,general-policy") || !leadKept() || !policyKept() {
		t.Fatalf("no terminal and no --replace: exit %d, kept both and said how to replace\n%s", code, out)
	}
	if code, out := run("setup", "--outdated", "--replace", "amp-like,nosuch"); code != 2 || !strings.Contains(out, `"nosuch": not a built-in template or shared prompt (changed now: amp-like, general-policy)`) || !leadKept() {
		t.Fatalf("a wrong name: exit %d; want a usage error and nothing replaced\n%s", code, out)
	}
	if code, out := run("setup", "--outdated", "--replace", "none"); code != 0 || !leadKept() || !policyKept() {
		t.Fatalf("--replace none: exit %d\n%s", code, out)
	}
	if code, out := run("setup", "--replace", "all"); code != 2 {
		t.Fatalf("--replace without --outdated: exit %d\n%s", code, out)
	}
	// What the user ticks on a terminal (the list is replaced by a fake that ticks amp-like).
	var buf bytes.Buffer
	e := &env{dir: dir, stdout: &buf, ask: func(_ []string, items []upgradeItem) ([]string, error) {
		if len(items) != 2 {
			t.Errorf("asked about %+v; want amp-like and general-policy", items)
		}
		return []string{"amp-like"}, nil
	}}
	if err := e.setup([]string{"--outdated"}); err != nil {
		t.Fatal(err)
	}
	want, _ := manifests.Builtin("amp-like")
	if got, _ := manifests.Resolve("amp-like", dir); got != want || !policyKept() || !strings.Contains(buf.String(), "kept as yours: general-policy") {
		t.Fatalf("after ticking amp-like: replaced=%v, policy kept=%v\n%s", got == want, policyKept(), buf.String())
	}
	if ents, _ := filepath.Glob(filepath.Join(dir, "templates", ".backup", "amp-like-*")); len(ents) != 1 {
		t.Fatalf("the backup of amp-like: %d entries", len(ents))
	}
	if code, out := run("setup", "--outdated", "--replace", "all"); code != 0 || policyKept() || !strings.Contains(out, "general-policy replaced") {
		t.Fatalf("--replace all: exit %d\n%s", code, out)
	}
	if strings.Contains(read(policy), "my own line") {
		t.Fatal("general-policy still has the user's text")
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "rules", ".backup")); len(ents) != 1 {
		t.Fatalf("the backup of general-policy: %d entries", len(ents))
	}
	if d, _ := manifests.Differing(dir); len(d) != 0 {
		t.Fatalf("still differing after everything was replaced: %+v", d)
	}
}

// The tick list: nothing is ticked until the user ticks it; enter hands back the ticked names, esc
// (or ctrl+c) hands back none; d reads the diff and esc comes back to the list, not out of it.
func TestPickKeys(t *testing.T) {
	items := []upgradeItem{{Name: "amp-like", Note: "you changed a"}, {Name: "slp", Note: "you changed b"}}
	diffOf := func(it upgradeItem) ([]diffLine, error) { return lineDiff("one\ntwo\n", "one\n"+it.Name+"\n", 2), nil }
	keys := map[string]tea.KeyPressMsg{
		"down": {Code: tea.KeyDown}, "space": {Code: tea.KeySpace, Text: " "}, "enter": {Code: tea.KeyEnter},
		"esc": {Code: tea.KeyEscape}, "d": {Code: 'd', Text: "d"}, "ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
	}
	press := func(m *pickModel, ks ...string) {
		t.Helper()
		for _, k := range ks {
			m.Update(keys[k])
		}
	}
	m := newPick(nil, items, diffOf)
	press(m, "enter")
	if got := m.Chosen(); len(got) != 0 {
		t.Fatalf("enter with nothing ticked: %v", got)
	}
	m = newPick(nil, items, diffOf)
	press(m, "down", "space", "enter")
	if got := m.Chosen(); !slices.Equal(got, []string{"slp"}) {
		t.Fatalf("down, space, enter: %v; want [slp]", got)
	}
	for _, quit := range []string{"esc", "ctrl+c"} {
		m = newPick(nil, items, diffOf)
		press(m, "space", quit)
		if got := m.Chosen(); len(got) != 0 {
			t.Fatalf("a tick then %s: %v; want none", quit, got)
		}
	}
	m = newPick(nil, items, diffOf)
	press(m, "down", "d")
	if m.pane == nil || !strings.Contains(m.View().Content, "+ slp") || !strings.Contains(m.View().Content, "- two") {
		t.Fatalf("d: the diff of the selected line is not shown:\n%s", m.View().Content)
	}
	press(m, "esc")
	if m.pane != nil || m.done {
		t.Fatal("esc in the diff must come back to the list")
	}
	press(m, "space", "enter")
	if got := m.Chosen(); !slices.Equal(got, []string{"slp"}) {
		t.Fatalf("after reading a diff: %v; want [slp]", got)
	}
}

// The reminder: once per version, and only when `setup --outdated` has something to do.
func TestRemindOncePerVersion(t *testing.T) {
	dir := t.TempDir()
	old := Version
	defer func() { Version = old }()
	Version = "v9.0.0"
	if _, err := manifests.Unpack(dir); err != nil {
		t.Fatal(err)
	}
	e := &env{dir: dir}
	if e.reminder() != "" {
		t.Fatal("a home with nothing to do was reminded")
	}
	lead := filepath.Join(dir, "templates", "amp-like", "prompts", "amp-like", "lead.md")
	if err := os.WriteFile(lead, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e.reminder() != "" {
		t.Fatal("reminded again in a version that was already looked at")
	}
	Version = "v9.0.1"
	if got := e.reminder(); got != "piggery v9.0.1: run piggery setup --outdated" {
		t.Fatalf("new version with a changed template: %q", got)
	}
	if e.reminder() != "" {
		t.Fatal("reminded twice in one version")
	}
}

// `update` replaces the binary, then runs the NEW binary's `setup --outdated` with the --replace it
// was given; --check runs nothing; at the latest version it runs setup itself.
func TestUpdateRunsSetupOutdated(t *testing.T) {
	skipOnWindows(t, "the new binary is a shell script")
	home, bin := t.TempDir(), t.TempDir()
	exe, ran := filepath.Join(bin, "piggery"), filepath.Join(bin, "ran")
	script := []byte("#!/bin/sh\necho \"$@\" > " + ran + "\n")
	sum := sha256.Sum256(script)
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	tag := "v0.2.0"
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": []map[string]string{
			{"name": asset, "browser_download_url": srv.URL + "/bin"}, {"name": "checksums.txt", "browser_download_url": srv.URL + "/sums"}}})
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, _ *http.Request) { w.Write(script) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
	})
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := Version
	defer func() { Version = old }()
	Version = "v0.1.0"
	update := func(args ...string) (string, error) {
		var out bytes.Buffer
		e := &env{dir: home, stdout: &out, upd: &updater{api: srv.URL + "/latest", exe: exe, goos: runtime.GOOS, goarch: runtime.GOARCH, client: srv.Client()}}
		err := e.update(args)
		return out.String(), err
	}
	if _, err := update("--check"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("--check ran setup")
	}
	if out, err := update("--replace", "amp-like"); err != nil || !strings.Contains(out, "updated v0.1.0 → v0.2.0") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(ran); strings.TrimSpace(string(b)) != "setup --outdated --replace=amp-like" {
		t.Fatalf("the new binary was run with %q", b)
	}
	// At the latest version this binary is the new one: it runs the second step itself.
	Version = "v0.2.0"
	os.Remove(ran)
	out, err := update()
	if err != nil || !strings.Contains(out, "already v0.2.0") || !strings.Contains(out, "up to date") {
		t.Fatalf("update at the latest version: %v\n%s", err, out)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the binary on disk was run instead of this one")
	}
	if _, err := os.Stat(filepath.Join(home, "templates")); err != nil {
		t.Fatal("setup --outdated did not refresh the home")
	}
}

// After `update` the first command starts the new daemon, whose start already brings the home up to date.
// It notes what it did (server.NoteHomeChanges); `setup --outdated` says it once, then it is gone.
func TestSetupSaysWhatTheDaemonDid(t *testing.T) {
	dir := t.TempDir()
	if _, err := manifests.Unpack(dir); err != nil { // as the daemon did: the setup below has nothing of its own to add
		t.Fatal(err)
	}
	cfg := server.ConfigPath(dir)
	server.NoteHomeChanges(dir, manifests.Unpacked{Added: []string{"amp-like"}, Updated: []string{"slp"}},
		[]server.Filled{{Path: cfg, Added: []string{"prompts: rules/general-policy.md for amp-like/*"}}})
	run := func() string {
		var out, errOut bytes.Buffer
		if code := Main(dir, []string{"setup", "--outdated"}, &out, &errOut); code != 0 {
			t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
		}
		return out.String()
	}
	out := run()
	for _, want := range []string{"templates: slp updated; amp-like added", "added to " + cfg + ": prompts: rules/general-policy.md for amp-like/*"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup --outdated does not say %q:\n%s", want, out)
		}
	}
	if out := run(); strings.Contains(out, "amp-like added") || strings.Contains(out, "general-policy.md for amp-like") {
		t.Errorf("said again:\n%s", out)
	}
}
