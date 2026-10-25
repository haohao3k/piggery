package server

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

// RoleSpawn is one entry of config.yaml `roles`: the harness, model and thinking level of the
// workers of the roles its key names, set by the Human instead of in the template, so replacing a
// template (template update --force) does not lose them. The key is a role as in `prompts`:
// `<template>/<role>`, `<template>/*`, `<role>` or `*`. Each field is read on its own: one left
// out falls to the next key that sets it (see roleSpawnFor), then to the template, then to the
// chain a spawn follows without any of this.
type RoleSpawn struct {
	Harness  string `yaml:"harness"`
	Model    string `yaml:"model"`
	Thinking string `yaml:"thinking"`
}

// checkRoleShape drops what LoadSettings can see is wrong without reading anything else: a key
// that is not a role pattern (or is `solo`, which no worker runs as), a harness piggery does not
// have, an entry left setting nothing. `inherit` reads as left out. It returns what stays and one
// warning per problem, naming the entry: a mistake there never stops the daemon.
func checkRoleShape(path string, roles map[string]RoleSpawn) (kept map[string]RoleSpawn, warns []string) {
	kept = map[string]RoleSpawn{}
	for _, pat := range slices.Sorted(maps.Keys(roles)) {
		at := fmt.Sprintf("%s: roles[%q]", path, pat)
		r := roles[pat]
		for _, f := range []*string{&r.Harness, &r.Model, &r.Thinking} {
			if *f == "inherit" {
				*f = ""
			}
		}
		switch {
		case !rolePatternOK(pat) || pat == soloRole:
			warns = append(warns, at+": want <template>/<role>, <template>/*, <role> or * (a worker has no solo role); entry skipped")
			continue
		case r.Harness != "" && !slices.Contains(local.Harnesses(), r.Harness):
			warns = append(warns, fmt.Sprintf("%s: harness %q: want one of %s; harness left out", at, r.Harness, strings.Join(local.Harnesses(), ", ")))
			r.Harness = ""
		}
		if r == (RoleSpawn{}) {
			warns = append(warns, at+": sets no harness, model or thinking; entry skipped")
			continue
		}
		kept[pat] = r
	}
	return kept, warns
}

// CheckRoles is the daemon's start-up check of the roles entries, after the built-in templates are
// unpacked: every key must name a template or a role some template has (a role a template update
// removed makes its entry useless). It returns the entries to keep and a warning naming each one
// dropped, as CheckPrompts does.
func CheckRoles(dir string, roles map[string]RoleSpawn) (kept map[string]RoleSpawn, warns []string) {
	if len(roles) == 0 {
		return roles, nil
	}
	tr, err := templateRoles(dir)
	if err != nil {
		return roles, []string{fmt.Sprintf("%s: roles not checked against the templates: %v", ConfigPath(dir), err)}
	}
	kept = map[string]RoleSpawn{}
	for _, pat := range slices.Sorted(maps.Keys(roles)) {
		if why := roleProblem(tr, pat); why != "" {
			warns = append(warns, fmt.Sprintf("%s: roles[%q]: %s; entry skipped", ConfigPath(dir), pat, why))
			continue
		}
		kept[pat] = roles[pat]
	}
	return kept, warns
}

// roleSpawnFor is the resolver for core.WithRoleSpawn. For each field on its own the first entry
// that sets it wins, in the order `<template>/<role>`, `<template>/*`, `<role>`, `*`; a field no
// entry sets uses the fork’s dual-lens default, then the template and the usual chain.
func roleSpawnFor(roles map[string]RoleSpawn) func(template, role string) core.SpawnPin {
	return func(template, role string) core.SpawnPin {
		var p core.SpawnPin
		for _, k := range []string{template + "/" + role, template + "/*", role, "*"} {
			if template == "" && strings.HasPrefix(k, "/") {
				continue
			}
			r := roles[k]
			p.Harness, p.Model, p.Thinking = cmp.Or(p.Harness, r.Harness), cmp.Or(p.Model, r.Model), cmp.Or(p.Thinking, r.Thinking)
		}
		h, m, t := manifests.DualLensRoute(template, role)
		p.Harness, p.Model, p.Thinking = cmp.Or(p.Harness, h), cmp.Or(p.Model, m), cmp.Or(p.Thinking, t)
		return p
	}
}

// What AddRoleSpawn did.
const (
	RoleAdded  = "added"  // the entry is in config.yaml
	RoleExists = "exists" // the file already has an entry for that key: left as it is
	RoleByHand = "byhand" // `roles` is written in a way it cannot place an entry in
)

// RoleEntry is the line of config.yaml `roles` for pattern: `"slp/lead": {model: gpt-5.5}`.
func RoleEntry(pattern string, r RoleSpawn) string {
	var f []string
	for _, kv := range [][2]string{{"harness", r.Harness}, {"model", r.Model}, {"thinking", r.Thinking}} {
		if kv[1] != "" {
			f = append(f, kv[0]+": "+yamlScalar(kv[1]))
		}
	}
	return fmt.Sprintf("%q: {%s}", pattern, strings.Join(f, ", "))
}

var plainScalar = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]*$`)

func yamlScalar(s string) string {
	if plainScalar.MatchString(s) && s != "true" && s != "false" && s != "null" {
		return s
	}
	return fmt.Sprintf("%q", s)
}

// AddRoleSpawn adds the entry for pattern to config.yaml's `roles`, keeping every byte of the file
// (a missing key is added at the end, `roles: {}` becomes a block). An entry for the same key is
// never overwritten. A flow mapping with entries is left for the Human (RoleByHand).
func AddRoleSpawn(dir, pattern string, r RoleSpawn) (state string, err error) {
	path := ConfigPath(dir)
	cur, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cur = nil
	} else if err != nil {
		return "", err
	}
	out, state, err := addRoleText(path, cur, pattern, r, false)
	if err != nil || state != RoleAdded {
		return state, err
	}
	return state, os.WriteFile(path, out, 0o600)
}

// addRoleText is AddRoleSpawn on the text of the file. With merge, an entry that exists gets the
// fields of r it does not set (RoleByHand when it cannot place them); without, it is left (RoleExists).
func addRoleText(path string, cur []byte, pattern string, r RoleSpawn, merge bool) (out []byte, state string, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(cur, &doc); err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	var root *yaml.Node
	if len(doc.Content) == 1 {
		root = doc.Content[0]
	}
	if root != nil && root.Kind != yaml.MappingNode {
		return nil, RoleByHand, nil
	}
	item := func(indent string) string { return indent + RoleEntry(pattern, r) + "\n" }
	lines := strings.SplitAfter(string(cur), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		lines[len(lines)-1] += "\n"
	}
	seq, next := mapKey(root, "roles"), 0 // next: line of the key after roles (0: none)
	if root != nil {
		for i := 0; i+2 < len(root.Content); i += 2 {
			if root.Content[i].Value == "roles" {
				next = root.Content[i+2].Line
			}
		}
	}
	switch {
	case seq == nil:
		lines = append(lines, "roles:\n"+item("  "))
	case seq.Kind != yaml.MappingNode:
		return nil, RoleByHand, nil
	case mapKey(seq, pattern) != nil:
		if !merge {
			return nil, RoleExists, nil
		}
		var ok bool
		if lines, ok = mergeRoleFields(lines, mapKey(seq, pattern), r); !ok {
			return nil, RoleByHand, nil
		}
	case seq.Style&yaml.FlowStyle != 0 && len(seq.Content) > 0:
		return nil, RoleByHand, nil
	case seq.Style&yaml.FlowStyle != 0: // roles: {} (a comment after it stays)
		l := lines[seq.Line-1]
		end := seq.Column - 1 + strings.Index(l[seq.Column-1:], "}") + 1
		lines[seq.Line-1] = strings.TrimRight(l[:seq.Column-1], " ") + l[end:] + item("  ")
	default: // after the mapping's last line: before the next key and the comments over it
		at := len(lines)
		if next > 0 {
			at = next - 1
		}
		for at > 0 && (strings.TrimSpace(lines[at-1]) == "" || strings.HasPrefix(lines[at-1], "#")) {
			at--
		}
		first := lines[seq.Content[0].Line-1]
		ins := item(first[:len(first)-len(strings.TrimLeft(first, " "))])
		lines = append(lines[:at], append([]string{ins}, lines[at:]...)...)
	}
	out = []byte(strings.Join(lines, ""))
	var check configFile // never write a file the daemon would refuse
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	if err := dec.Decode(&check); err != nil && !errors.Is(err, io.EOF) {
		return nil, "", configError(path, out, err)
	}
	return out, RoleAdded, nil
}

// mergeRoleFields adds to the entry ent (the value of an existing key of `roles`) the fields of r it
// does not write: before the closing brace of a one-line flow mapping, or as lines after the last
// line of a block mapping. ok is false for any other shape, and when ent already writes one of the
// fields (even as inherit): a duplicate key would be an error.
func mergeRoleFields(lines []string, ent *yaml.Node, r RoleSpawn) ([]string, bool) {
	if ent.Kind != yaml.MappingNode || len(ent.Content) == 0 {
		return lines, false
	}
	var add []string
	for _, kv := range [][2]string{{"harness", r.Harness}, {"model", r.Model}, {"thinking", r.Thinking}} {
		if kv[1] == "" {
			continue
		}
		if mapKey(ent, kv[0]) != nil {
			return lines, false
		}
		add = append(add, kv[0]+": "+yamlScalar(kv[1]))
	}
	last := ent.Content[len(ent.Content)-1]
	if last.Kind != yaml.ScalarNode {
		return lines, false
	}
	if ent.Style&yaml.FlowStyle == 0 { // block: the new lines go after the last value's line
		indent := strings.Repeat(" ", ent.Content[0].Column-1)
		ins := make([]string, len(add))
		for i, a := range add {
			ins[i] = indent + a + "\n"
		}
		return append(lines[:last.Line], append(ins, lines[last.Line:]...)...), true
	}
	if ent.Line != last.Line { // flow over several lines
		return lines, false
	}
	l := lines[last.Line-1]
	at := 0
	for c := last.Column; c > 1; c-- { // the value's start, the column in characters
		_, w := utf8.DecodeRuneInString(l[at:])
		at += w
	}
	switch last.Style {
	case yaml.DoubleQuotedStyle, yaml.SingleQuotedStyle:
		q := l[at]
		for at++; at < len(l) && l[at] != q; at++ {
			if q == '"' && l[at] == '\\' {
				at++
			}
		}
		at++
	default:
		at += len(last.Value)
	}
	if at > len(l) {
		return lines, false
	}
	i := strings.IndexByte(l[at:], '}')
	if i < 0 {
		return lines, false
	}
	at += i
	lines[last.Line-1] = strings.TrimRight(l[:at], " ") + ", " + strings.Join(add, ", ") + l[at:]
	return lines, true
}
