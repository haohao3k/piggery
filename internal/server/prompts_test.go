package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sting8k/piggery/manifests"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A role gets the files whose entries name it (`<role>` in any template, `<template>/<role>` in
// that one only, `<template>/*` every role of that template, `*` every role and solo, `solo` for
// solo sessions), in config order, each under a heading naming the file, a file several matching
// entries name only once;
// the file is read at each call, so an edit shows in the next card; a file that is gone is warned
// about and left out of the card, which is still built.
func TestSharedPromptsForARole(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "rules", "code.md"), "code rules\n")
	writeFile(t, filepath.Join(dir, "deleg.md"), "delegate\n")
	writeFile(t, filepath.Join(dir, "all.md"), "everyone\n")
	writeFile(t, filepath.Join(dir, "se.md"), "se only\n")
	abs := filepath.Join(t.TempDir(), "abs.md")
	writeFile(t, abs, "absolute")
	var warned []string
	f := promptsFor(dir, []PromptEntry{
		{File: "rules/code.md", Roles: []string{"executor", "solo"}},
		{File: "deleg.md", Roles: []string{"se/supervisor", "solo"}},
		{File: abs, Roles: []string{"executor"}},
		{File: "all.md", Roles: []string{"*"}},
		{File: "se.md", Roles: []string{"se/*", "executor"}},
		{File: "all.md", Roles: []string{"executor"}}, // the same file again: no second copy
	}, func(m string) { warned = append(warned, m) })
	got := func(template, role string) string { return f(template, role) }
	if s := got("se", "executor"); !strings.Contains(s, "## Shared prompt from rules/code.md\ncode rules\n") || !strings.Contains(s, "absolute") ||
		strings.Index(s, "code rules") > strings.Index(s, "absolute") || strings.Contains(s, "delegate") {
		t.Fatalf("executor: %q", s)
	}
	if s := got("se", "supervisor"); !strings.Contains(s, "delegate") || strings.Contains(s, "code rules") {
		t.Fatalf("se/supervisor: %q", s)
	}
	if s := got("other", "supervisor"); strings.Contains(s, "delegate") {
		t.Fatalf("<template>/<role> leaked to another template: %q", s)
	}
	if s := got("", "solo"); !strings.Contains(s, "code rules") || !strings.Contains(s, "delegate") {
		t.Fatalf("solo: %q", s)
	}
	if s := got("se", "supervisor"); !strings.Contains(s, "everyone") || !strings.Contains(s, "se only") {
		t.Fatalf("se/supervisor: %q; want * and se/*", s)
	}
	if s := got("se", "executor"); strings.Count(s, "everyone") != 1 || strings.Count(s, "se only") != 1 {
		t.Fatalf("se/executor: %q; want each wildcard file once", s)
	}
	if s := got("other", "supervisor"); !strings.Contains(s, "everyone") || strings.Contains(s, "se only") {
		t.Fatalf("other/supervisor: %q; want * but not se/*", s)
	}
	if s := got("", "solo"); !strings.Contains(s, "everyone") || strings.Contains(s, "se only") {
		t.Fatalf("solo: %q; want * but not se/*", s)
	}
	writeFile(t, filepath.Join(dir, "rules", "code.md"), "edited rules")
	if s := got("se", "executor"); !strings.Contains(s, "edited rules") {
		t.Fatalf("after an edit: %q", s)
	}
	os.Remove(filepath.Join(dir, "rules", "code.md"))
	if s := got("se", "executor"); !strings.Contains(s, "absolute") || strings.Contains(s, "rules") || len(warned) != 1 || !strings.Contains(warned[0], "code.md") {
		t.Fatalf("a file that is gone: card %q, warnings %q; want it left out with one warning naming the file", s, warned)
	}
}

// A mistake in the prompts never stops the daemon: each bad entry, or bad role of an entry, is left
// out with one warning naming it (no file, no roles, a role of a bad form, an unreadable file, a
// template that is no manifest's `template:`, a role that template lacks, a role in no template),
// and the rest of the settings load. The built-in roles are found; `solo` needs no template.
func TestBadSharedPromptsAreWarnedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	if _, err := manifests.Unpack(dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "rules.md"), "x")
	writeFile(t, ConfigPath(dir), `harness: pi
prompts:
  - {file: rules.md, roles: [solo, "a/b/c"]}
  - {file: rules.md}
  - {roles: [solo]}
  - {file: rules.md, roles: [solo, lead-peer/peer]}
  - {file: rules.md, roles: ["*", "lead-peer/*", "exec*", "*/peer"]}
`)
	set, err := LoadSettings(dir)
	if err != nil || set.Harness != "pi" || len(set.Warnings) != 5 {
		t.Fatalf("settings %+v, %v; want the daemon's settings with 5 shape warnings (a glob is only * or <template>/*)", set, err)
	}
	for i, want := range []string{"prompts[0]", "prompts[1]", "prompts[2]", `"exec*"`, `"*/peer"`} {
		if !strings.Contains(set.Warnings[i], want) {
			t.Errorf("warning %d = %q; want it to name %s", i, set.Warnings[i], want)
		}
	}
	if len(set.Prompts) != 3 || !slices.Equal(set.Prompts[0].Roles, []string{"solo"}) || !slices.Equal(set.Prompts[2].Roles, []string{"*", "lead-peer/*"}) {
		t.Fatalf("kept %+v; want the good entry and the good role of the first", set.Prompts)
	}
	kept, warns := CheckPrompts(dir, []PromptEntry{
		{File: "rules.md", Roles: []string{"solo", "lead-peer/peer", "supervisor"}},
		{File: "nope.md", Roles: []string{"solo"}},
		{File: "rules.md", Roles: []string{"solo", "nosuch/peer", "lead-peer/supervisor", "executr", "*", "lead-peer/*", "nosuch/*"}},
	})
	if len(kept) != 2 || len(kept[0].Roles) != 3 || !slices.Equal(kept[1].Roles, []string{"solo", "*", "lead-peer/*"}) || len(warns) != 5 {
		t.Fatalf("kept %+v, warnings %q", kept, warns)
	}
	for i, want := range []string{"prompts[1]", "nosuch", "lead-peer/supervisor", "executr", "nosuch/*"} {
		if !strings.Contains(warns[i], want) {
			t.Errorf("warning %d = %q; want it to name %s", i, warns[i], want)
		}
	}
}
