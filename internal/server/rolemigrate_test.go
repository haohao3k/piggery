package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/sting8k/piggery/manifests"
)

func writeTemplate(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(manifests.Dir(dir), name, manifests.ManifestFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// effective is the harness, model and thinking every role of a template runs with: config.yaml `roles`
// first, field by field, then what the template itself says.
func effective(t *testing.T, dir, manifestPath string) map[string]RoleSpawn {
	t.Helper()
	set, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	text := readFile(t, manifestPath)
	pins, err := manifests.SpawnPinsOf([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Template string `yaml:"template"`
		Roles    map[string]any
	}
	if err := yaml.Unmarshal([]byte(text), &head); err != nil {
		t.Fatal(err)
	}
	out := map[string]RoleSpawn{}
	for role := range head.Roles {
		c := roleSpawnFor(set.Roles)(head.Template, role)
		r := RoleSpawn{Harness: c.Harness, Model: c.Model, Thinking: c.Thinking}
		if r.Harness == "" {
			r.Harness = pins[role]["harness"]
		}
		if r.Model == "" {
			r.Model = pins[role]["model"]
		}
		if r.Thinking == "" {
			r.Thinking = pins[role]["thinking"]
		}
		out[role] = r
	}
	return out
}

// MigrateSpawn leaves what every role runs with as it was. The wildcard `*: {thinking: high}` already
// beats the template's thinking: max, so no exact entry is written for it; an exact entry that sets the
// model beats the template's, so only the lines leave; one that lacks a field the template sets gets it
// (the same line, other keys kept); a role with no entry gets one at the end of `roles`. Every other
// byte of config.yaml and of the manifest stays, the originals are copied to templates/.backup/migrate-*,
// the move is reported once, and a second run does nothing.
func TestMigrateSpawn(t *testing.T) {
	dir := t.TempDir()
	cfg := "# my config\nharness: pi\nroles:\n  \"*\": {thinking: high}   # wildcard\n  \"ctf/lead\": {model: fixed}\n  \"ctf/solver\": {model: mine}\ngate: open\n"
	if err := os.WriteFile(ConfigPath(dir), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	man := "# ctf\ntemplate: ctf\nroles:\n" +
		"  lead:\n    spawn:    # how\n      harness: codex\n      model: ignored\n      allow_tools: []\n" +
		"  solver:\n    spawn:\n      model: m1\n      thinking: max\n" +
		"  scout:\n    spawn:\n      model: m3\n      thinking: low\n"
	path := writeTemplate(t, dir, "ctf-ops", man)
	before := effective(t, dir, path)

	filled, errs := MigrateSpawn(dir)
	if len(errs) != 0 || len(filled) != 1 || filled[0].Kind != KindMoved || len(filled[0].Added) != 1 || filled[0].Added[0] != "ctf-ops (lead, scout, solver)" {
		t.Fatalf("filled %+v, errs %v", filled, errs)
	}
	if got := effective(t, dir, path); len(got) != 3 || got["lead"] != before["lead"] || got["solver"] != before["solver"] || got["scout"] != before["scout"] {
		t.Fatalf("effective settings changed:\n before %+v\n after  %+v", before, got)
	}
	// "ctf/lead" had model: fixed (kept), gains harness: codex; solver and the wildcard-covered thinking write nothing;
	// scout gets an entry (its thinking: low is under the wildcard's high, so only the model).
	wantCfg := "# my config\nharness: pi\nroles:\n  \"*\": {thinking: high}   # wildcard\n  \"ctf/lead\": {model: fixed, harness: codex}\n  \"ctf/solver\": {model: mine}\n  \"ctf/scout\": {model: m3}\ngate: open\n"
	if got := readFile(t, ConfigPath(dir)); got != wantCfg {
		t.Fatalf("config.yaml:\n got %q\nwant %q", got, wantCfg)
	}
	if got := readFile(t, path); strings.Contains(got, "model:") || strings.Contains(got, "thinking:") || !strings.Contains(got, "# how") || !strings.Contains(got, "allow_tools: []") || !strings.HasPrefix(got, "# ctf\n") {
		t.Fatalf("manifest:\n%s", got)
	}
	stamp := filled[0].Path
	if readFile(t, filepath.Join(stamp, "config.yaml")) != cfg || readFile(t, filepath.Join(stamp, "ctf-ops", manifests.ManifestFile)) != man {
		t.Fatalf("the copies in %s are not the originals", stamp)
	}
	if again, errs := MigrateSpawn(dir); len(again) != 0 || len(errs) != 0 {
		t.Fatalf("second run: %+v, %v", again, errs)
	}
}

// What it cannot place exactly it leaves, with the template's own warning still true: `roles` written in
// flow style with entries, a spawn written in flow style, a harness piggery does not have. Nothing is
// written, no backup is made, and each is named. A template that can be moved still is.
func TestMigrateSpawnLeavesWhatItCannotPlace(t *testing.T) {
	dir := t.TempDir()
	flowCfg := "roles: {\"*\": {thinking: high}}\n"
	if err := os.WriteFile(ConfigPath(dir), []byte(flowCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	flowSpawn := "template: fs\nroles:\n  a:\n    spawn: {model: m}\n"
	badHarness := "template: bh\nroles:\n  a:\n    spawn:\n      harness: nosuch\n"
	needsEntry := "template: ne\nroles:\n  a:\n    spawn:\n      model: m\n"
	covered := "template: cv\nroles:\n  a:\n    spawn:\n      thinking: max\n" // the wildcard has thinking: only lines leave
	paths := map[string]string{}
	for name, text := range map[string]string{"flow-spawn": flowSpawn, "bad-harness": badHarness, "needs-entry": needsEntry, "covered": covered} {
		paths[name] = writeTemplate(t, dir, name, text)
	}
	filled, errs := MigrateSpawn(dir)
	if len(errs) != 3 {
		t.Fatalf("errs %v; want flow-spawn, bad-harness and needs-entry named", errs)
	}
	for name, text := range map[string]string{"flow-spawn": flowSpawn, "bad-harness": badHarness, "needs-entry": needsEntry} {
		if readFile(t, paths[name]) != text {
			t.Errorf("%s was changed", name)
		}
	}
	if readFile(t, ConfigPath(dir)) != flowCfg {
		t.Errorf("config.yaml was changed: %q", readFile(t, ConfigPath(dir)))
	}
	if len(filled) != 1 || len(filled[0].Added) != 1 || !strings.HasPrefix(filled[0].Added[0], "covered") || strings.Contains(readFile(t, paths["covered"]), "thinking") {
		t.Fatalf("the template that could move: %+v", filled)
	}
}
