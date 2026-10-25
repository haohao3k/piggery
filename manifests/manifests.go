// Package manifests keeps the team templates: their home (~/.piggery/templates), the built-ins
// embedded here (this directory's *.yaml and prompts) unpacked into it, and making a manifest
// self-contained (instructions_file inlined).
package manifests

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

//go:embed *.yaml prompts rules
var builtin embed.FS

// Templates live in one place, <home>/templates/<name>/:
// manifest.yaml and the prompt files it references. found, templates and team up <name> look
// only there. The built-ins embedded here are unpacked there (Unpack) and edited in place.

// Dir is where templates live under the piggery dir home.
func Dir(home string) string { return filepath.Join(home, "templates") }

// ManifestFile is a template's manifest inside its directory.
const ManifestFile = "manifest.yaml"

// recordFile keeps, per built-in template, the hash of each file Unpack wrote.
const recordFile = ".builtin.json"

func validName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid template name %q", name)
	}
	return nil
}

// Resolve returns template name from <home>/templates/<name>/manifest.yaml as a
// self-contained manifest. No such template -> an error wrapping fs.ErrNotExist.
func Resolve(name, home string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	p := filepath.Join(Dir(home), name, ManifestFile)
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("no template %q in %s: %w", name, Dir(home), fs.ErrNotExist)
		}
		return "", err
	}
	return Inline(p)
}

// Listed is a template in the home and its directory.
type Listed struct{ Name, From string }

// List returns the templates in <home>/templates, by name: every directory with a manifest.
func List(home string) ([]Listed, error) {
	entries, err := os.ReadDir(Dir(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Listed
	for _, e := range entries {
		dir := filepath.Join(Dir(home), e.Name())
		if validName(e.Name()) != nil || !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err == nil {
			out = append(out, Listed{Name: e.Name(), From: dir})
		}
	}
	return out, nil
}

// Builtin returns the built-in template name as a self-contained manifest (the solo limits
// come from lead-peer); none -> an error wrapping fs.ErrNotExist. found never uses it: it reads the home.
func Builtin(name string) (string, error) {
	p := name + ".yaml"
	raw, err := builtin.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", name, fs.ErrNotExist)
	}
	return inline(p, raw, func(file string) ([]byte, error) {
		if path.IsAbs(file) {
			return nil, fmt.Errorf("built-in template: absolute instructions_file %s", file)
		}
		return builtin.ReadFile(path.Join(path.Dir(p), file))
	})
}

// Builtins names the built-in templates.
func Builtins() []string {
	names, _ := builtinFiles(builtin)
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// builtinFiles maps each built-in template in src (<name>.yaml at its root) to its files, as
// written in a template directory: manifest.yaml and each instructions_file (relative path).
func builtinFiles(src fs.FS) (map[string]map[string][]byte, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, err
	}
	out := map[string]map[string][]byte{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || e.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(src, e.Name())
		if err != nil {
			return nil, err
		}
		files := map[string][]byte{ManifestFile: raw}
		var m struct {
			Roles map[string]struct {
				File string `yaml:"instructions_file"`
			} `yaml:"roles"`
		}
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("built-in %s: %w", name, err)
		}
		for _, r := range m.Roles {
			if r.File == "" {
				continue
			}
			if path.IsAbs(r.File) || strings.HasPrefix(path.Clean(r.File), "..") {
				return nil, fmt.Errorf("built-in %s: instructions_file %s must be inside the template", name, r.File)
			}
			b, err := fs.ReadFile(src, r.File)
			if err != nil {
				return nil, fmt.Errorf("built-in %s: %w", name, err)
			}
			files[path.Clean(r.File)] = b
		}
		out[name] = files
	}
	return out, nil
}

// Unpack brings the built-in templates into <home>/templates. A template is one block: the
// record (.builtin.json) holds the hash of every file Unpack wrote. A template seen for the first
// time is written whole. After that, a directory that is still exactly what was recorded (no file
// changed, removed or added; dot files do not count) is piggery's: it is replaced whole by the
// current version. One the user changed in any way is kept whole, the files they did not touch
// too, so it never becomes a mix of versions; Differing lists it. A built-in template whose
// directory the user removed is not written again. A same-named template the user made before its
// built-in existed is left alone. A template this binary no longer has built in (a release
// dropped it) is cleaned out the same way: removed whole when untouched, kept whole when not, and
// it leaves the record either way. It reports the templates it wrote.
func Unpack(home string) (Unpacked, error) { return unpack(home, builtin) }

// Unpacked is what Unpack wrote: templates that were not there (Added) and templates brought to a
// newer version (Updated), by name.
type Unpacked struct{ Added, Updated []string }

func unpack(home string, src fs.FS) (out Unpacked, err error) {
	files, err := builtinFiles(src)
	if err != nil {
		return out, err
	}
	root := Dir(home)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return out, err
	}
	record, err := readRecord(root)
	if err != nil {
		return out, err
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		tf, dir := files[name], filepath.Join(root, name)
		rec, known := record[name]
		want := hashesOf(tf)
		_, statErr := os.Stat(dir)
		switch {
		case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
			return out, statErr
		case statErr != nil && known: // the user removed it
		case statErr == nil && !known: // the user's own template of that name
		case statErr != nil: // seen for the first time
			if err := writeBlock(dir, tf); err != nil {
				return out, err
			}
			record[name] = want
			out.Added = append(out.Added, name)
		default:
			if d, _, err := diffDir(dir, want); err != nil {
				return out, err
			} else if len(d) == 0 { // already this version (also when edited to it by hand): piggery's again
				record[name] = want
			} else if d, _, err := diffDir(dir, rec); err != nil {
				return out, err
			} else if len(d) == 0 { // as piggery wrote it, an older version
				if err := replaceBlock(dir, rec, tf); err != nil {
					return out, err
				}
				record[name] = want
				out.Updated = append(out.Updated, name)
			} // else the user's: kept whole
		}
	}
	for name, rec := range record {
		if _, still := files[name]; still {
			continue
		}
		dir := filepath.Join(root, name)
		if d, _, err := diffDir(dir, rec); err != nil {
			return out, err
		} else if len(d) == 0 { // as piggery wrote it: gone whole; else it stays whole, the user's now
			if err := dropBuiltin(dir, rec); err != nil {
				return out, err
			}
		}
		delete(record, name)
	}
	return out, writeRecord(filepath.Join(root, recordFile), record)
}

// hashesOf is the record entry of a built-in's files.
func hashesOf(tf map[string][]byte) map[string]string {
	h := make(map[string]string, len(tf))
	for rel, b := range tf {
		h[rel] = hash(b)
	}
	return h
}

// writeBlock writes the files tf into dir (made if missing).
func writeBlock(dir string, tf map[string][]byte) error {
	for rel, content := range tf {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if cur, err := os.ReadFile(p); err == nil && bytes.Equal(cur, content) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// replaceBlock brings dir, which is exactly rec (what Unpack recorded), to the files tf: what a
// newer version dropped is deleted, the rest written.
func replaceBlock(dir string, rec map[string]string, tf map[string][]byte) error {
	gone := map[string]string{}
	for rel, h := range rec {
		if _, in := tf[rel]; !in {
			gone[rel] = h
		}
	}
	if err := dropBuiltin(dir, gone); err != nil {
		return err
	}
	return writeBlock(dir, tf)
}

// readRecord is <root>/.builtin.json: template -> file -> sha256 written (none: empty).
func readRecord(root string) (map[string]map[string]string, error) {
	record := map[string]map[string]string{}
	if b, err := os.ReadFile(filepath.Join(root, recordFile)); err == nil {
		if err := json.Unmarshal(b, &record); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(root, recordFile), err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return record, nil
}

// Update states: what Update did with a template's directory.
const (
	UpdateCreated  = "created"  // there was no directory: written from the built-in
	UpdateCurrent  = "current"  // the directory is the built-in as it is now
	UpdateUpdated  = "updated"  // piggery's own copy of an older version: brought to this one
	UpdateKept     = "kept"     // it differs and force is off: left alone
	UpdateReplaced = "replaced" // it differs and force is on: moved to Backup, the built-in written
)

// Updated is what Update did with template Name. Diff lists, for kept and replaced, how the
// directory differed ("changed manifest.yaml", "removed prompts/x.md", "added notes.md"). Yours:
// the directory was not piggery's (a template of the user's own with the built-in's name).
type Updated struct {
	Name, State, Backup string
	Diff                []string
	Yours               bool
}

// Update brings built-in template name in <home>/templates to the version this binary has, and
// records its files' hashes so a later Unpack goes on updating what the user did not change. A
// missing directory is written; one equal to the built-in is left; one that differs (a file
// changed, removed or added; dot files are not looked at) is left too unless force, which moves
// it whole to <home>/templates/.backup/<name>-<time> first. Unpack alone never does this: a file
// the user changed never gets a new version, and a directory the user removed is not written again.
func Update(home, name string, force bool) (Updated, error) {
	files, err := builtinFiles(builtin)
	if err != nil {
		return Updated{}, err
	}
	tf, ok := files[name]
	if !ok {
		return Updated{}, fmt.Errorf("no built-in template %q (built-ins: %s)", name, strings.Join(Builtins(), ", "))
	}
	root := Dir(home)
	record, err := readRecord(root)
	if err != nil {
		return Updated{}, err
	}
	dir := filepath.Join(root, name)
	u := Updated{Name: name}
	_, known := record[name]
	want := hashesOf(tf)
	diff, exists, err := diffDir(dir, want)
	var own []string // how the directory differs from what Unpack recorded
	if err == nil && known && exists {
		own, _, err = diffDir(dir, record[name])
	}
	switch {
	case err != nil:
		return u, err
	case !exists:
		u.State = UpdateCreated
	case len(diff) == 0:
		u.State = UpdateCurrent
	case known && len(own) == 0: // piggery's own, an older version
		if err = replaceBlock(dir, record[name], tf); err != nil {
			return u, err
		}
		u.State = UpdateUpdated
	default:
		u.Diff, u.Yours = diff, !known
		if !force {
			u.State = UpdateKept
			return u, nil
		}
		u.Backup = backupPath(filepath.Join(root, ".backup"), name)
		if err := os.MkdirAll(filepath.Dir(u.Backup), 0o700); err != nil {
			return u, err
		}
		if err := os.Rename(dir, u.Backup); err != nil {
			return u, err
		}
		u.State = UpdateReplaced
	}
	if u.State == UpdateCreated || u.State == UpdateReplaced {
		if err := writeBlock(dir, tf); err != nil {
			return u, err
		}
	}
	record[name] = want // the hash of each file as it is now: Unpack goes on updating it
	return u, writeRecord(filepath.Join(root, recordFile), record)
}

// Differing lists the built-in templates whose directory in <home>/templates is the user's, not
// piggery's: changed in any way since Unpack wrote it (Diff says how), or a template of the user's
// own with the built-in's name (Yours; Diff is then against the built-in). One that is piggery's
// own older version is not listed; Unpack brings it up. State is UpdateKept. By name.
func Differing(home string) ([]Updated, error) { return differing(home, builtin) }

func differing(home string, src fs.FS) ([]Updated, error) {
	files, err := builtinFiles(src)
	if err != nil {
		return nil, err
	}
	root := Dir(home)
	record, err := readRecord(root)
	if err != nil {
		return nil, err
	}
	var out []Updated
	for _, name := range slices.Sorted(maps.Keys(files)) {
		dir := filepath.Join(root, name)
		rec, known := record[name]
		diff, exists, err := diffDir(dir, hashesOf(files[name]))
		switch {
		case err != nil:
			return nil, err
		case !exists || len(diff) == 0:
			continue
		case known:
			if diff, _, err = diffDir(dir, rec); err != nil {
				return nil, err
			} else if len(diff) == 0 {
				continue
			}
		}
		out = append(out, Updated{Name: name, State: UpdateKept, Diff: diff, Yours: !known})
	}
	return out, nil
}

// backupPath is a free name under dir for a copy of name taken now: name-<time>, with -2, -3 when
// two replacements fall in one second.
func backupPath(dir, name string) string {
	base := filepath.Join(dir, name+"-"+time.Now().Format("20060102-150405"))
	p := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s-%d", base, i)
	}
}

// Pair is a file in which the user's copy differs from the built-in's: Yours is nil when the copy
// lacks the file, Builtin is nil when the user added it.
type Pair struct {
	Rel            string
	Yours, Builtin []byte
}

// Compare lists, by path, the files of built-in template name in which its directory in the home
// differs from the built-in: the same set diffDir reports.
func Compare(home, name string) ([]Pair, error) {
	files, err := builtinFiles(builtin)
	if err != nil {
		return nil, err
	}
	tf, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("no built-in template %q", name)
	}
	dir := filepath.Join(Dir(home), name)
	diff, _, err := diffDir(dir, hashesOf(tf))
	if err != nil {
		return nil, err
	}
	var out []Pair
	for _, d := range diff {
		verb, rel, _ := strings.Cut(d, " ")
		pr := Pair{Rel: rel}
		if verb != "added" {
			pr.Builtin = tf[rel]
		}
		if verb != "removed" {
			if pr.Yours, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
				return nil, err
			}
		}
		out = append(out, pr)
	}
	return out, nil
}

// RulesDiffering lists the shared prompt files piggery ships (BuiltinPrompts, as "rules/x.md") whose
// copy in the home is not the built-in text: after UnpackRules that is the user's own edit.
func RulesDiffering(home string) ([]string, error) {
	var out []string
	for _, bp := range BuiltinPrompts {
		want, err := builtin.ReadFile(bp.File)
		if err != nil {
			return nil, err
		}
		switch cur, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(bp.File))); {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, err
		case !bytes.Equal(cur, want):
			out = append(out, bp.File)
		}
	}
	return out, nil
}

// CompareRule is the one Pair of shared prompt file (as "rules/x.md") against the built-in text.
func CompareRule(home, file string) (Pair, error) {
	want, err := builtin.ReadFile(file)
	if err != nil {
		return Pair{}, fmt.Errorf("no shared prompt %q", file)
	}
	cur, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(file)))
	return Pair{Rel: file, Yours: cur, Builtin: want}, err
}

// ReplaceRule moves the user's copy of shared prompt file (as "rules/x.md") to
// <home>/rules/.backup/<name>-<time> and writes the built-in text, recording its hash so a later
// UnpackRules goes on updating it. It returns the backup path.
func ReplaceRule(home, file string) (backup string, err error) {
	want, err := builtin.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("no shared prompt %q", file)
	}
	p := filepath.Join(home, filepath.FromSlash(file))
	backup = backupPath(filepath.Join(filepath.Dir(p), ".backup"), filepath.Base(p))
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(p, backup); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, want, 0o600); err != nil {
		return backup, err
	}
	recPath := filepath.Join(home, "rules", recordFile)
	rec := map[string]string{}
	if b, err := os.ReadFile(recPath); err == nil {
		if err := json.Unmarshal(b, &rec); err != nil {
			return backup, fmt.Errorf("%s: %w", recPath, err)
		}
	}
	rec[file] = hash(want)
	return backup, writeRecord(recPath, rec)
}

// SpawnPins are the deprecated spawn.harness, .model and .thinking the manifest at path still sets,
// by role, keys "harness", "model", "thinking"; a field left out or `inherit` is not in it. They now
// belong in config.yaml `roles`.
func SpawnPins(path string) (map[string]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pins, err := SpawnPinsOf(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return pins, nil
}

// spawnFields are the spawn settings that now belong in config.yaml `roles`.
var spawnFields = []string{"harness", "model", "thinking"}

// SpawnPinsOf is SpawnPins for the text of a manifest.
func SpawnPinsOf(raw []byte) (map[string]map[string]string, error) {
	var m struct {
		Roles map[string]struct {
			Spawn map[string]any `yaml:"spawn"`
		} `yaml:"roles"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for role, r := range m.Roles {
		for _, k := range spawnFields {
			if v, ok := r.Spawn[k].(string); ok && v != "" && v != "inherit" {
				if out[role] == nil {
					out[role] = map[string]string{}
				}
				out[role][k] = v
			}
		}
	}
	return out, nil
}

// DropSpawnPins is the text of a manifest without the lines that set spawn.harness, .model or
// .thinking to a value (the ones SpawnPinsOf returns), every other byte as it was. A spawn left with
// nothing goes too (or becomes `spawn: {}` when a comment sits inside it). It refuses (error) what it
// cannot do exactly: a role or spawn written as a flow mapping, a value not on its key's line, or a
// result whose parsed content is not the original's without those settings.
func DropSpawnPins(text []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(text, &doc); err != nil {
		return nil, err
	}
	var roles *yaml.Node
	if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		roles = valueOf(doc.Content[0], "roles")
	}
	lines := strings.SplitAfter(string(text), "\n")
	drop := map[int]bool{} // 1-based line numbers to delete
	if roles != nil && roles.Kind == yaml.MappingNode {
		for r := 0; r+1 < len(roles.Content); r += 2 {
			role := roles.Content[r+1]
			spawn := valueOf(role, "spawn")
			if spawn == nil || spawn.Kind != yaml.MappingNode {
				continue
			}
			var pins []*yaml.Node // key nodes
			for i := 0; i+1 < len(spawn.Content); i += 2 {
				k, v := spawn.Content[i], spawn.Content[i+1]
				if slices.Contains(spawnFields, k.Value) && v.Kind == yaml.ScalarNode && v.Value != "" && v.Value != "inherit" && v.Tag == "!!str" {
					pins = append(pins, k)
				}
			}
			if len(pins) == 0 {
				continue
			}
			if roles.Style&yaml.FlowStyle != 0 || role.Style&yaml.FlowStyle != 0 || spawn.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("role %s: spawn is written as a flow mapping", roles.Content[r].Value)
			}
			for i := 0; i+1 < len(spawn.Content); i += 2 {
				if k, v := spawn.Content[i], spawn.Content[i+1]; slices.Contains(pins, k) {
					if v.Line != k.Line {
						return nil, fmt.Errorf("role %s: spawn.%s has its value on another line", roles.Content[r].Value, k.Value)
					}
					drop[k.Line] = true
				}
			}
			if len(pins)*2 == len(spawn.Content) { // nothing else in spawn
				key := valueKey(role, "spawn")
				first, last := key.Line, spawn.Content[len(spawn.Content)-1].Line
				clean := true
				for l := first + 1; l <= last; l++ {
					clean = clean && (drop[l] || strings.TrimSpace(lines[l-1]) == "")
				}
				if clean {
					drop[first] = true
				} else if m := emptySpawnLine.FindStringSubmatch(lines[first-1]); m != nil {
					lines[first-1] = m[1] + " {}" + m[2] + "\n"
					if strings.HasSuffix(lines[first-1], "\r\n") || strings.HasSuffix(strings.TrimSuffix(m[0], "\n"), "\r") {
						lines[first-1] = m[1] + " {}" + strings.TrimSuffix(m[2], "\r") + "\r\n"
					}
				} else {
					return nil, fmt.Errorf("role %s: cannot tidy the empty spawn", roles.Content[r].Value)
				}
			}
		}
	}
	if len(drop) == 0 {
		return text, nil
	}
	var out strings.Builder
	for i, l := range lines {
		if !drop[i+1] {
			out.WriteString(l)
		}
	}
	got := []byte(out.String())
	if !sameWithoutPins(text, got) {
		return nil, errors.New("the manifest would change in more than the spawn settings")
	}
	return got, nil
}

// emptySpawnLine is a `spawn:` line with nothing but a comment after it.
var emptySpawnLine = regexp.MustCompile(`^(\s*spawn:)\s*(#.*)?$`)

// sameWithoutPins is whether got parses to what orig does with spawn.harness|model|thinking values taken
// out (a spawn left empty counts the same as one that is gone).
func sameWithoutPins(orig, got []byte) bool {
	var a, b any
	if yaml.Unmarshal(orig, &a) != nil || yaml.Unmarshal(got, &b) != nil {
		return false
	}
	return reflect.DeepEqual(stripPins(a, true), stripPins(b, false))
}

// stripPins removes the pins (when pins is set) and then every empty spawn, and makes a role left with
// nothing an empty one, from a parsed manifest.
func stripPins(doc any, pins bool) any {
	top, _ := doc.(map[string]any)
	roles, _ := top["roles"].(map[string]any)
	for name, r := range roles {
		role, _ := r.(map[string]any)
		if spawn, _ := role["spawn"].(map[string]any); spawn != nil {
			if pins {
				for _, k := range spawnFields {
					if v, ok := spawn[k].(string); ok && v != "" && v != "inherit" {
						delete(spawn, k)
					}
				}
			}
			if len(spawn) == 0 {
				delete(role, "spawn")
			}
		}
		if role != nil && len(role) == 0 { // a role left with nothing reads as an empty one
			roles[name] = nil
		}
	}
	return doc
}

// valueOf is the value of key in mapping n, nil when n has no such key.
func valueOf(n *yaml.Node, key string) *yaml.Node {
	if k := valueKey(n, key); k != nil {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i] == k {
				return n.Content[i+1]
			}
		}
	}
	return nil
}

// valueKey is the key node named key of mapping n.
func valueKey(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i]
		}
	}
	return nil
}

// diffDir compares template directory dir with the files want (path -> sha256: the built-in's, or
// what Unpack recorded): the one definition of "changed" for Unpack, Update, Differing and
// `template list`. A file differing or missing, or one added (dot files do not count) is a
// difference. exists is false when dir is not there; diff is sorted.
func diffDir(dir string, want map[string]string) (diff []string, exists bool, err error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	for rel, h := range want {
		switch cur, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); {
		case errors.Is(err, fs.ErrNotExist):
			diff = append(diff, "removed "+rel)
		case err != nil:
			return nil, true, err
		case hash(cur) != h:
			diff = append(diff, "changed "+rel)
		}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch rel, _ := filepath.Rel(dir, p); {
		case err != nil:
			return err
		case rel == ".":
		case strings.HasPrefix(d.Name(), "."): // .DS_Store and the like
			if d.IsDir() {
				return filepath.SkipDir
			}
		case !d.IsDir():
			if _, in := want[filepath.ToSlash(rel)]; !in {
				diff = append(diff, "added "+filepath.ToSlash(rel))
			}
		}
		return nil
	})
	slices.Sort(diff)
	return diff, true, err
}

// dropBuiltin deletes the files of rec that still have the hash recorded, then the directories they
// leave empty, dir included. Anything else in dir stays.
func dropBuiltin(dir string, rec map[string]string) error {
	for rel, want := range rec {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		cur, err := os.ReadFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return err
		case hash(cur) != want:
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		for d := filepath.Dir(p); d != filepath.Dir(dir) && strings.HasPrefix(d, dir); d = filepath.Dir(d) {
			if os.Remove(d) != nil { // not empty (a file of the user's): it stays
				break
			}
		}
	}
	return nil
}

// syncFile brings built-in file p (key in rec, the hashes Unpack wrote) to content: written when
// missing and never written, updated while it still has the hash recorded; a file the user
// changed or removed, or one that was never ours, is left alone.
func syncFile(p string, content []byte, rec map[string]string, key string) error {
	want := hash(content)
	cur, err := os.ReadFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if _, written := rec[key]; written {
			return nil // written before, removed by the user
		}
	case err != nil:
		return err
	case hash(cur) == want:
		rec[key] = want // already this version (also when edited to it by hand): ours again
		return nil
	case hash(cur) != rec[key]:
		return nil // changed by the user (or not ours)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		return err
	}
	rec[key] = want
	return nil
}

func writeRecord(p string, record any) error {
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// BuiltinPrompt is a shared prompt piggery ships: File (relative to the piggery dir, as a
// config.yaml `prompts` entry names it) for Roles.
type BuiltinPrompt struct {
	File  string
	Roles []string
}

// BuiltinPrompts are the shared prompts piggery ships, for the roles of its built-in templates.
var BuiltinPrompts = []BuiltinPrompt{{File: "rules/general-policy.md", Roles: []string{"lead-peer/*", "slp/*"}}}

// releasedRoles are the roles every released piggery offered its shared prompt to, before
// <home>/rules/.builtin.json recorded them: a home that has the file but no record of its roles
// was offered these.
var releasedRoles = []string{"lead-peer/*", "slp/*"}

// UnpackRules brings the BuiltinPrompts files into <home>/rules the way Unpack does a template's
// files (<home>/rules/.builtin.json records them). taken are the prompts that became piggery's in
// this call (written, or found with the same text): the caller adds their config entry once, so
// an entry or a file the user removed later is not brought back. more are, for a prompt that was
// piggery's before and whose file is still there, the roles a newer piggery offers it to and this
// home was never offered: the caller adds them to the user's entry once, so a role the user
// removes stays removed. The roles offered are recorded under "roles:<file>".
func UnpackRules(home string) (taken, more []BuiltinPrompt, err error) {
	recPath := filepath.Join(home, "rules", recordFile)
	rec := map[string]string{}
	if b, err := os.ReadFile(recPath); err == nil {
		if err := json.Unmarshal(b, &rec); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", recPath, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	for _, bp := range BuiltinPrompts {
		content, err := builtin.ReadFile(bp.File)
		if err != nil {
			return nil, nil, err
		}
		_, known := rec[bp.File]
		p := filepath.Join(home, filepath.FromSlash(bp.File))
		if err := syncFile(p, content, rec, bp.File); err != nil {
			return nil, nil, err
		}
		rolesKey := "roles:" + bp.File
		switch _, now := rec[bp.File]; {
		case now && !known:
			taken = append(taken, bp)
			rec[rolesKey] = strings.Join(bp.Roles, ",")
		case known:
			if _, err := os.Stat(p); err != nil {
				continue // removed by the user: offer it to no one
			}
			offered := releasedRoles
			if v, ok := rec[rolesKey]; ok {
				offered = strings.Split(v, ",")
			}
			var add []string
			for _, r := range bp.Roles {
				if !slices.Contains(offered, r) {
					add = append(add, r)
				}
			}
			if len(add) > 0 {
				more = append(more, BuiltinPrompt{File: bp.File, Roles: add})
			}
			rec[rolesKey] = strings.Join(append(slices.Clone(offered), add...), ",")
		}
	}
	if len(rec) == 0 {
		return nil, nil, nil
	}
	return taken, more, writeRecord(recPath, rec)
}

// New writes the current built-in from as a new template name in the home, for the user to
// edit. It refuses a name that exists; the copy is the user's (Unpack never touches it).
func New(home, name, from string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	files, err := builtinFiles(builtin)
	if err != nil {
		return "", err
	}
	tf, ok := files[from]
	if !ok {
		return "", fmt.Errorf("no built-in template %q (built-ins: %s)", from, strings.Join(Builtins(), ", "))
	}
	dir := filepath.Join(Dir(home), name)
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("template %q already exists at %s", name, dir)
		}
		return "", err
	}
	for rel, content := range tf {
		if rel == ManifestFile { // template is the team's name at team up: the copy's own name
			content = renameTemplate(content, name)
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, content, 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// topTemplate is the value of the manifest's top-level template: line (comments after it are kept).
var topTemplate = regexp.MustCompile(`(?m)^(template:[ \t]*)[^\s#]+`)

// renameTemplate sets the top-level template to name, changing nothing else in the file.
func renameTemplate(manifest []byte, name string) []byte {
	loc := topTemplate.FindSubmatchIndex(manifest)
	if loc == nil {
		return manifest
	}
	out := append([]byte{}, manifest[:loc[3]]...)
	out = append(out, name...)
	return append(out, manifest[loc[1]:]...)
}

// legacyKey is the top-level model: line a manifest had before its name key became template:.
var legacyKey = regexp.MustCompile(`(?m)^model:`)

// MigrateKey renames the top-level `model:` of each manifest in the home's templates to
// `template:`, changing no other byte, and returns the manifests it changed. A manifest that has a
// `template:` already is left alone. One that is still an unedited built-in (its hash is the one
// Unpack recorded) has its record moved to the new hash, so it keeps updating like before.
func MigrateKey(home string) ([]string, error) {
	ls, err := List(home)
	if err != nil {
		return nil, err
	}
	recPath := filepath.Join(Dir(home), recordFile)
	record := map[string]map[string]string{}
	if b, err := os.ReadFile(recPath); err == nil {
		if err := json.Unmarshal(b, &record); err != nil {
			return nil, fmt.Errorf("%s: %w", recPath, err)
		}
	}
	var changed []string
	recorded := false
	for _, l := range ls {
		p := filepath.Join(l.From, ManifestFile)
		cur, err := os.ReadFile(p)
		if err != nil {
			return changed, err
		}
		if !legacyKey.Match(cur) || topTemplate.Match(cur) {
			continue
		}
		loc := legacyKey.FindIndex(cur)
		out := append(append(append([]byte{}, cur[:loc[0]]...), "template:"...), cur[loc[1]:]...)
		info, err := os.Stat(p)
		if err != nil {
			return changed, err
		}
		if err := os.WriteFile(p, out, info.Mode().Perm()); err != nil {
			return changed, err
		}
		changed = append(changed, p)
		if rec := record[l.Name]; rec != nil && rec[ManifestFile] == hash(cur) {
			rec[ManifestFile], recorded = hash(out), true
		}
	}
	if recorded {
		b, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return changed, err
		}
		return changed, os.WriteFile(recPath, append(b, '\n'), 0o600)
	}
	return changed, nil
}

// MigrateTimers renames the key silent_for to its new name working_for in the timers of each manifest
// in the home's templates (built-in, edited or the user's), changing no other byte, and returns the
// manifests it changed. A rule that has both keys is left alone (loading it is still refused), and so
// is a key that is not written as a plain word (quoted): the old name is still read, with a warning.
// A built-in the user edited stays edited: nothing here looks at the record.
func MigrateTimers(home string) ([]string, error) {
	ls, err := List(home)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, l := range ls {
		p := filepath.Join(l.From, ManifestFile)
		cur, err := os.ReadFile(p)
		if err != nil {
			return changed, err
		}
		out := renameSilentFor(cur)
		if out == nil {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return changed, err
		}
		if err := os.WriteFile(p, out, info.Mode().Perm()); err != nil {
			return changed, err
		}
		changed = append(changed, p)
	}
	return changed, nil
}

// renameSilentFor is text with silent_for renamed working_for in the rules of `timers:` that do not
// have working_for; nil when there is nothing to rename. Only the key's own bytes change.
func renameSilentFor(text []byte) []byte {
	var doc yaml.Node
	if yaml.Unmarshal(text, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	var offsets []int // byte offset of each silent_for key to rename
	top := doc.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		if top[i].Value != "timers" || top[i+1].Kind != yaml.SequenceNode {
			continue
		}
		for _, rule := range top[i+1].Content {
			if rule.Kind != yaml.MappingNode {
				continue
			}
			var silent *yaml.Node
			working := false
			for k := 0; k+1 < len(rule.Content); k += 2 {
				switch rule.Content[k].Value {
				case "silent_for":
					silent = rule.Content[k]
				case "working_for":
					working = true
				}
			}
			if silent == nil || working {
				continue
			}
			if off := byteOffset(text, silent.Line, silent.Column); off >= 0 && bytes.HasPrefix(text[off:], []byte("silent_for")) {
				offsets = append(offsets, off)
			}
		}
	}
	if len(offsets) == 0 {
		return nil
	}
	slices.Sort(offsets)
	out := make([]byte, 0, len(text)+len(offsets))
	prev := 0
	for _, off := range offsets {
		out = append(append(out, text[prev:off]...), "working_for"...)
		prev = off + len("silent_for")
	}
	return append(out, text[prev:]...)
}

// byteOffset is the offset in text of a yaml.Node position: line and column count from 1, the column
// in characters, not bytes. -1 when the position is not in text.
func byteOffset(text []byte, line, column int) int {
	off := 0
	for ; line > 1; line-- {
		i := bytes.IndexByte(text[off:], '\n')
		if i < 0 {
			return -1
		}
		off += i + 1
	}
	for ; column > 1; column-- {
		if off >= len(text) {
			return -1
		}
		_, w := utf8.DecodeRune(text[off:])
		off += w
	}
	return off
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Inline reads the manifest file at p and replaces each role's instructions_file (relative to
// the manifest's directory) with instructions holding the file's text, so core only ever sees
// a self-contained manifest.
func Inline(p string) (string, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return inline(p, raw, func(file string) ([]byte, error) {
		if !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(p), file)
		}
		return os.ReadFile(file)
	})
}

// inline does Inline for manifest text raw (named p in errors), reading each
// instructions_file through read.
func inline(p string, raw []byte, read func(file string) ([]byte, error)) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("manifest %s: %w", p, err)
	}
	roles := mapValue(&doc, "roles")
	if roles == nil || roles.Kind != yaml.MappingNode {
		return string(raw), nil
	}
	changed := false
	for i := 0; i+1 < len(roles.Content); i += 2 {
		name, role := roles.Content[i].Value, roles.Content[i+1]
		if role.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(role.Content); j += 2 {
			if role.Content[j].Value != "instructions_file" {
				continue
			}
			if mapValue(role, "instructions") != nil {
				return "", fmt.Errorf("manifest %s: roles.%s has both instructions and instructions_file", p, name)
			}
			text, err := read(role.Content[j+1].Value)
			if err != nil {
				return "", fmt.Errorf("manifest %s: roles.%s.instructions_file: %w", p, name, err)
			}
			role.Content[j].Value = "instructions"
			role.Content[j+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(text)}
			changed = true
		}
	}
	if !changed {
		return string(raw), nil
	}
	out, err := yaml.Marshal(&doc)
	return string(out), err
}

// mapValue returns the value node of key in a mapping (or a document holding one), or nil.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
