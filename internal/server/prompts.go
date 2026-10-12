package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sting8k/piggery/manifests"
)

// A shared prompt is a text file of the Human's (code rules, how to organise a project) that goes
// into the role card of every role it names, in every template and harness: config.yaml `prompts`.
// A role is `<role>` (that role in every template), `<template>/<role>` (the manifest's `template:`
// names the template, as `template new` sets it), `solo` (solo sessions), `<template>/*` (every
// role of that template) or `*` (every role of every template, and solo sessions).
type PromptEntry struct {
	File  string   `yaml:"file"`
	Roles []string `yaml:"roles"`
}

// soloRole is the name of a solo session in a prompt entry.
const soloRole = "solo"

// promptPath is where an entry's file is: relative to the piggery dir, or absolute.
func promptPath(dir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(dir, file)
}

// promptsFor adds built-in agent guidance, then matching Human prompt files in config order.
// Files are read for each card; an unreadable file is warned about and skipped.
func promptsFor(dir string, entries []PromptEntry, warn func(msg string)) func(template, role string) string {
	return func(template, role string) string {
		var b strings.Builder
		b.WriteString("\n" + strings.TrimSpace(manifests.AgentInstructions()) + "\n")
		seen := map[string]bool{} // a file several entries name is added once, at its first entry
		for _, e := range entries {
			path := promptPath(dir, e.File)
			if seen[path] || !slices.ContainsFunc(e.Roles, func(r string) bool {
				return r == "*" || r == role || (template != "" && (r == template+"/"+role || r == template+"/*"))
			}) {
				continue
			}
			seen[path] = true
			text, err := os.ReadFile(path)
			if err != nil {
				warn(fmt.Sprintf("shared prompt %s left out of the card of %s/%s: %v", e.File, template, role, err))
				continue
			}
			fmt.Fprintf(&b, "\n## Shared prompt from %s\n%s\n", e.File, strings.TrimSpace(string(text)))
		}
		return b.String()
	}
}

// checkPromptShape drops what LoadSettings can see is wrong without reading anything else: an entry
// with no file or no role, a role that is not `<role>` or `<template>/<role>` (the rest of its entry
// stays). It returns the entries to keep and one warning per problem, naming the entry.
func checkPromptShape(path string, entries []PromptEntry) (kept []PromptEntry, warns []string) {
	for i, e := range entries {
		at := fmt.Sprintf("%s: prompts[%d]", path, i)
		if e.File == "" {
			warns = append(warns, at+": no file; entry skipped")
			continue
		}
		var roles []string
		for _, r := range e.Roles {
			tpl, role, two := strings.Cut(r, "/")
			glob := strings.Contains(r, "*") && r != "*" && !(two && role == "*" && !strings.Contains(tpl, "*")) // only `*` and `<template>/*`
			if r == "" || glob || strings.Contains(role, "/") || (two && (tpl == "" || role == "")) {
				warns = append(warns, fmt.Sprintf("%s (%s): role %q: want <role>, <template>/<role>, <template>/*, * or solo; role skipped", at, e.File, r))
				continue
			}
			roles = append(roles, r)
		}
		if len(roles) == 0 {
			warns = append(warns, fmt.Sprintf("%s (%s): no roles; entry skipped", at, e.File))
			continue
		}
		kept = append(kept, PromptEntry{File: e.File, Roles: roles})
	}
	return kept, warns
}

// CheckPrompts is the daemon's start-up check of the prompts entries, after the built-in templates
// are unpacked: every file reads, every `<template>` is the `template:` of a template in the piggery
// dir, and every role is in a template (`solo` needs none). It returns the entries to keep (a bad
// entry or a bad role of one is dropped) and a warning naming each problem: a mistake in the
// Human's prompts never stops the daemon.
func CheckPrompts(dir string, entries []PromptEntry) (kept []PromptEntry, warns []string) {
	if len(entries) == 0 {
		return nil, nil
	}
	roles, err := templateRoles(dir) // template -> role names
	if err != nil {
		return entries, []string{fmt.Sprintf("%s: prompts not checked against the templates: %v", ConfigPath(dir), err)}
	}
	for i, e := range entries {
		at := fmt.Sprintf("%s: prompts[%d] (%s)", ConfigPath(dir), i, e.File)
		if _, err := os.ReadFile(promptPath(dir, e.File)); err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v; entry skipped", at, err))
			continue
		}
		var good []string
		for _, r := range e.Roles {
			if why := roleProblem(roles, r); why != "" {
				warns = append(warns, fmt.Sprintf("%s: %q: %s; role skipped", at, r, why))
				continue
			}
			good = append(good, r)
		}
		if len(good) > 0 {
			kept = append(kept, PromptEntry{File: e.File, Roles: good})
		}
	}
	return kept, warns
}

// roleProblem says why role r (from an entry) matches nothing, or "" when it can.
func roleProblem(roles map[string][]string, r string) string {
	tpl, role, two := strings.Cut(r, "/")
	switch {
	case r == soloRole, r == "*":
	case two:
		rs, ok := roles[tpl]
		if !ok {
			return fmt.Sprintf("no template has template: %s (piggery templates lists them; <template> is the manifest's template:)", tpl)
		}
		if role != "*" && !slices.Contains(rs, role) {
			return fmt.Sprintf("template %s has no role %q (roles: %s)", tpl, role, strings.Join(rs, ", "))
		}
	default:
		for _, rs := range roles {
			if slices.Contains(rs, r) {
				return ""
			}
		}
		return "the role is in no template"
	}
	return ""
}

// templateRoles reads the manifests in the piggery dir's templates: each one's `template:` and role
// names. A manifest that does not read is an error naming it.
func templateRoles(dir string) (map[string][]string, error) {
	ls, err := manifests.List(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, l := range ls {
		p := filepath.Join(l.From, manifests.ManifestFile)
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var m struct {
			Template string               `yaml:"template"`
			Model    string               `yaml:"model"` // before the key was renamed
			Roles    map[string]yaml.Node `yaml:"roles"`
		}
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if m.Template == "" {
			m.Template = m.Model
		}
		for r := range m.Roles {
			out[m.Template] = append(out[m.Template], r)
		}
		slices.Sort(out[m.Template])
	}
	return out, nil
}
