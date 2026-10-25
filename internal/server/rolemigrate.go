package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

// MigrateSpawn moves the spawn.harness, .model and .thinking that templates in the home still set
// into config.yaml `roles`, so that the effective harness, model and thinking of every role stay what
// they were. Per field: when no entry of `roles` (exact, <template>/*, <role>, *) sets it, it is
// written as `<template>/<role>` (an entry of that key gets the field it lacks); when one does, the
// template's value never had an effect. Either way the lines leave the manifest; every other byte
// of the manifest and of config.yaml stays. A template is moved whole or not at all: one whose
// manifest or whose place in config.yaml it cannot edit exactly (flow style), one whose harness is
// not one piggery has, or one whose move it could not show to change nothing, is left as it is, with
// its warning, and named in errs. Before anything is written, the manifests and config.yaml it changes
// are copied to templates/.backup/migrate-<time>/ (the path is in the result). Config first: a stop
// halfway leaves entries that say what the manifests do.
//
// The daemon reads `roles` once, at start. So this runs at the daemon's start (which then reads the file
// again) and in `piggery setup` when no daemon is running.
func MigrateSpawn(dir string) (filled []Filled, errs []error) {
	ls, err := manifests.List(dir)
	if err != nil {
		return nil, []error{err}
	}
	type tpl struct {
		name, path string
		key        string
		text       []byte
		pins       map[string]map[string]string
	}
	var todo []tpl
	for _, l := range ls {
		path := filepath.Join(l.From, manifests.ManifestFile)
		text, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pins, err := manifests.SpawnPinsOf(text)
		if err != nil || len(pins) == 0 {
			continue
		}
		var head struct {
			Template string `yaml:"template"`
		}
		_ = yaml.Unmarshal(text, &head)
		if head.Template == "" {
			errs = append(errs, fmt.Errorf("%s: no template: name, so its model choices are not moved into config.yaml roles", path))
			continue
		}
		todo = append(todo, tpl{filepath.Base(l.From), path, head.Template, text, pins})
	}
	if len(todo) == 0 {
		return nil, nil
	}
	cfgPath := ConfigPath(dir)
	cfg, err := os.ReadFile(cfgPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, []error{err}
	}
	if _, err := LoadSettings(dir); err != nil { // a file the daemon refuses is not ours to edit: the error is told where it is read
		return nil, nil
	}
	origCfg := cfg
	roles, _ := rolesOf(cfg)
	planned := map[string]string{} // "<pattern> <field>" -> value this run puts in config
	type moved struct {
		tpl  tpl
		text []byte
	}
	var done []moved
	skip := func(t tpl, why string) {
		errs = append(errs, fmt.Errorf("%s: model choices not moved into config.yaml roles (%s); add them there by hand", t.path, why))
	}
templates:
	for _, t := range todo {
		text, err := manifests.DropSpawnPins(t.text)
		if err != nil {
			skip(t, err.Error())
			continue
		}
		next, nextPlanned := cfg, maps.Clone(planned)
		for _, role := range slices.Sorted(maps.Keys(t.pins)) {
			pat, add := t.key+"/"+role, RoleSpawn{}
			for field, v := range t.pins[role] {
				if field == "harness" && !slices.Contains(local.Harnesses(), v) {
					skip(t, fmt.Sprintf("%s: harness %q is not one of %s", role, v, strings.Join(local.Harnesses(), ", ")))
					continue templates
				}
				if p, ok := nextPlanned[pat+" "+field]; ok { // another template of this name put one there this run
					if p != v {
						skip(t, fmt.Sprintf("%s: %s differs from another template named %s", role, field, t.key))
						continue templates
					}
					continue
				}
				if setByConfig(roles, t.key, role, field) {
					continue // config wins: the template's value never had an effect
				}
				nextPlanned[pat+" "+field] = v
				switch field {
				case "harness":
					add.Harness = v
				case "model":
					add.Model = v
				case "thinking":
					add.Thinking = v
				}
			}
			if add == (RoleSpawn{}) {
				continue
			}
			out, state, err := addRoleText(cfgPath, next, pat, add, true)
			if err != nil || state != RoleAdded {
				skip(t, "config.yaml roles is written in a way piggery cannot add to: "+RoleEntry(pat, add))
				continue templates
			}
			next = out
		}
		after, ok := rolesOf(next)
		if !ok || !sameEffect(roles, after, t.key, t.pins) {
			skip(t, "could not show that the move changes nothing")
			continue
		}
		cfg, planned, roles = next, nextPlanned, after
		done = append(done, moved{t, text})
	}
	if len(done) == 0 {
		return nil, errs
	}
	stamp := backupDir(dir)
	if err := os.MkdirAll(stamp, 0o700); err != nil {
		return nil, append(errs, err)
	}
	if origCfg != nil {
		if err := os.WriteFile(filepath.Join(stamp, "config.yaml"), origCfg, 0o600); err != nil {
			return nil, append(errs, err)
		}
	}
	for _, m := range done {
		d := filepath.Join(stamp, m.tpl.name)
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, append(errs, err)
		}
		if err := os.WriteFile(filepath.Join(d, manifests.ManifestFile), m.tpl.text, 0o600); err != nil {
			return nil, append(errs, err)
		}
	}
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		return nil, append(errs, err)
	}
	var names []string
	for _, m := range done {
		info, err := os.Stat(m.tpl.path)
		if err != nil {
			return filled, append(errs, err)
		}
		if err := os.WriteFile(m.tpl.path, m.text, info.Mode().Perm()); err != nil {
			return filled, append(errs, err)
		}
		names = append(names, fmt.Sprintf("%s (%s)", m.tpl.name, strings.Join(slices.Sorted(maps.Keys(m.tpl.pins)), ", ")))
	}
	return []Filled{{Path: stamp, Added: names, Kind: KindMoved}}, errs
}

// rolesOf is the entries of `roles` in the text of config.yaml, as the daemon reads them (an entry
// that cannot work is not in it). ok is false when the text is not a config file.
func rolesOf(text []byte) (map[string]RoleSpawn, bool) {
	var raw configFile
	dec := yaml.NewDecoder(bytes.NewReader(text))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	kept, _ := checkRoleShape("", raw.Roles)
	return kept, true
}

// setByConfig is whether some entry of roles that applies to role of the template sets field.
func setByConfig(roles map[string]RoleSpawn, template, role, field string) bool {
	for _, k := range []string{template + "/" + role, template + "/*", role, "*"} {
		r := roles[k]
		if map[string]string{"harness": r.Harness, "model": r.Model, "thinking": r.Thinking}[field] != "" {
			return true
		}
	}
	return false
}

// sameEffect is whether, for every role with pins, harness, model and thinking come out the same with
// the old config and the template's own values as with the new config and none.
func sameEffect(before, after map[string]RoleSpawn, template string, pins map[string]map[string]string) bool {
	old, now := roleSpawnFor(before), roleSpawnFor(after)
	for role, p := range pins {
		o, n := old(template, role), now(template, role)
		for field, v := range p {
			switch field {
			case "harness":
				if o.Harness == "" {
					o.Harness = v
				}
			case "model":
				if o.Model == "" {
					o.Model = v
				}
			case "thinking":
				if o.Thinking == "" {
					o.Thinking = v
				}
			}
		}
		if o != n {
			return false
		}
	}
	return true
}

// backupDir is a free templates/.backup/migrate-<time> (with -2, -3 for two in one second).
func backupDir(dir string) string {
	base := filepath.Join(manifests.Dir(dir), ".backup", "migrate-"+time.Now().Format("20060102-150405"))
	p := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s-%d", base, i)
	}
}
