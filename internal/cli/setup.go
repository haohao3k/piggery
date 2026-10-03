package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/internal/server"
	"github.com/sting8k/piggery/manifests"
)

// setup:
//   - `piggery setup` writes the worker profiles (~/.piggery/harness/{pi,claude,codex,omp,dsh}.json, an
//     existing one kept unless --force), the templates and the daemon's config.yaml (only its
//     missing keys, with their defaults), then shows where each harness and the config stand;
//   - `piggery setup <pi|claude|codex|omp|dsh|paseo>` adds piggery to that harness, or its plugin to
//     Paseo (a second run changes nothing);
//   - `piggery setup --refresh` reapplies the embedded assets to integrations already installed and
//     safely refreshes built-in templates, without writing worker profiles;
//   - `piggery setup remove <name>` takes out what setup added.
func (e *env) setup(args []string) error {
	fs := e.flags("setup")
	ext := fs.String("ext", "", "a checkout's pi extension (extensions/pi) instead of the one in this binary")
	force := fs.Bool("force", false, "overwrite an existing profile")
	paseoHome := fs.String("paseo-home", "", "the Paseo daemon home setup paseo installs into (default: Paseo's own)")
	outdated := fs.Bool("outdated", false, "bring every installed piggery integration that is outdated up to date (takes no other argument)")
	refresh := fs.Bool("refresh", false, "refresh every installed piggery integration and built-in template (takes no other argument)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *outdated && *refresh {
		return fmt.Errorf("%w: setup --outdated and setup --refresh are mutually exclusive", errUsage)
	}
	if *outdated && len(pos) != 0 {
		return fmt.Errorf("%w: setup --outdated takes no argument (only --paseo-home)", errUsage)
	}
	if *refresh && len(pos) != 0 {
		return fmt.Errorf("%w: setup --refresh takes no argument (only --paseo-home)", errUsage)
	}
	if *refresh && *ext != "" {
		return fmt.Errorf("%w: setup --refresh cannot be used with --ext", errUsage)
	}
	usage := fmt.Errorf("%w: setup [pi|claude|codex|omp|dsh|paseo] | setup remove <pi|claude|codex|omp|dsh|paseo> [--ext PATH] [--paseo-home PATH] [--force]", errUsage)
	self, err := selfPath()
	if err != nil {
		return err
	}
	extDir := "" // the binary's copy of the pi extension, unless --ext names a checkout
	if *ext != "" {
		if extDir, err = extensionDir(*ext); err != nil {
			return err
		}
	}
	o := setupOpts{dir: e.dir, self: self, ext: extDir, paseoHome: *paseoHome}
	if o.paseoHome != "" {
		if o.paseoHome, err = filepath.Abs(o.paseoHome); err != nil {
			return err
		}
	}
	switch {
	case *outdated:
		return e.updateOutdated(o)
	case *refresh:
		return e.refresh(o)
	case len(pos) == 1:
		if t, ok := targetNamed(pos[0]); ok {
			return e.say(t.install(o))
		}
		return usage
	case len(pos) == 2 && pos[0] == "remove":
		if t, ok := targetNamed(pos[1]); ok {
			return e.say(t.remove(o))
		}
		return usage
	case len(pos) != 0:
		return usage
	}
	index := filepath.Join(local.PiExtDir(e.dir), "index.ts")
	if extDir != "" {
		index = filepath.Join(extDir, "index.ts")
	}
	// Profiles: a missing one is written (every one with --force); an existing one only gets the
	// keys it lacks, below, like every config file piggery owns.
	for _, p := range []struct {
		path, what string
		def        any
	}{
		{local.ProfilePath(e.dir), "pi workers", local.DefaultProfile(index)},
		{local.ClaudeProfilePath(e.dir), "Claude Code workers", local.DefaultClaudeProfile},
		{local.CodexProfilePath(e.dir), "Codex workers", local.DefaultCodexProfile},
		{local.OmpProfilePath(e.dir), "omp workers", local.DefaultOmpProfile},
		{local.DshProfilePath(e.dir), "dsh workers", local.DefaultDshProfile},
	} {
		if w, err := writeJSON(p.path, p.def, *force); err != nil {
			return err
		} else if w {
			fmt.Fprintf(e.stdout, "wrote %s (%s)\n", p.path, p.what)
		}
	}
	if err := manifests.Unpack(e.dir); err != nil {
		return fmt.Errorf("templates: %w", err)
	}
	fmt.Fprintf(e.stdout, "templates in %s\n", manifests.Dir(e.dir))
	filled, errs := server.EnsureFiles(e.dir)
	for _, f := range filled {
		fmt.Fprintf(e.stdout, "added to %s: %s (defaults)\n", f.Path, strings.Join(f.Added, ", "))
	}
	for _, err := range errs {
		fmt.Fprintf(e.stdout, "! %v\n", err)
	}
	fmt.Fprintln(e.stdout)
	for _, st := range harnessStates(o) {
		fmt.Fprintln(e.stdout, st.line())
		for _, p := range st.Problems {
			fmt.Fprintf(e.stdout, "  ! %s\n", p)
		}
	}
	fmt.Fprintln(e.stdout, server.ConfigStatus(e.dir))
	return nil
}

// refresh runs each target's bounded refresh handler for every integration that is already
// installed, regardless of its integration version. Handlers compare embedded assets and only
// rewrite managed files while preserving host registration, enablement, unrelated config and
// profiles. Built-in templates use the same safe Unpack semantics as ordinary setup. All selected
// work is attempted before a failure is returned.
func (e *env) refresh(o setupOpts) error {
	var failed []string
	if err := manifests.Unpack(e.dir); err != nil {
		fmt.Fprintf(e.stdout, "templates: NOT refreshed: %v\n", err)
		failed = append(failed, "templates")
	} else {
		overrides, err := builtinTemplateOverrides(e.dir)
		if err != nil {
			fmt.Fprintf(e.stdout, "templates: unable to verify refreshed built-ins: %v\n", err)
			failed = append(failed, "templates")
		} else if len(overrides) > 0 {
			fmt.Fprintf(e.stdout, "templates refreshed in %s; preserved local overrides: %s\n",
				manifests.Dir(e.dir), strings.Join(overrides, ", "))
		} else {
			fmt.Fprintf(e.stdout, "templates refreshed in %s\n", manifests.Dir(e.dir))
		}
	}

	list := installedIntegrations(o.dir)
	if len(list) == 0 {
		fmt.Fprintln(e.stdout, "piggery integrations: none installed")
	}
	for _, i := range list {
		t, ok := targetNamed(i.Name)
		if !ok || t.refresh == nil {
			continue
		}
		msg, err := t.refresh(o)
		if err != nil {
			fmt.Fprintf(e.stdout, "%s: NOT refreshed: %v\n", i.Name, err)
			failed = append(failed, i.Name)
			continue
		}
		fmt.Fprintln(e.stdout, msg)
	}
	if len(failed) > 0 {
		return fmt.Errorf("not refreshed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// builtinTemplateOverrides reports built-in names whose self-contained manifest after Unpack is
// not the embedded one. Unpack deliberately keeps user-edited, removed, or pre-existing
// same-named templates; those are useful local overrides rather than refresh failures.
func builtinTemplateOverrides(home string) ([]string, error) {
	var overrides []string
	for _, name := range manifests.Builtins() {
		want, err := manifests.Builtin(name)
		if err != nil {
			return nil, fmt.Errorf("builtin %s: %w", name, err)
		}
		got, err := manifests.Resolve(name, home)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				overrides = append(overrides, name)
				continue
			}
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		if got != want {
			overrides = append(overrides, name)
		}
	}
	return overrides, nil
}

// updateOutdated runs `setup <name>` for every integration that is installed and outdated (pi, omp
// and dsh too, in case the daemon is not running to do it), one after the other; a failure is named
// and the exit is non-zero, but the others still run. Not installed: untouched.
func (e *env) updateOutdated(o setupOpts) error {
	list := outdatedIntegrations(o.dir, false)
	if len(list) == 0 {
		fmt.Fprintln(e.stdout, "piggery integrations are up to date")
		return nil
	}
	var failed []string
	for _, i := range list {
		t, ok := targetNamed(i.Name)
		if !ok {
			continue
		}
		install := t.install
		// Skill drift is also an update trigger now. It must not turn automatic Codex
		// maintenance into permission to re-enable a disabled or unregistered integration.
		if i.Name == "codex" {
			install = t.refresh
		}
		msg, err := install(o)
		if err != nil {
			fmt.Fprintf(e.stdout, "%s: NOT updated (%s): %v\n", i.Name, i.detail(), err)
			failed = append(failed, i.Name)
			continue
		}
		if strings.HasPrefix(msg, i.Name+": skipped") {
			fmt.Fprintln(e.stdout, msg)
			continue
		}
		fmt.Fprintf(e.stdout, "%s: updated (%s)\n%s\n", i.Name, i.detail(), msg)
	}
	if len(failed) > 0 {
		return fmt.Errorf("not updated: %s", strings.Join(failed, ", "))
	}
	return nil
}

func (e *env) say(msg string, err error) error {
	if err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, msg)
	return nil
}

// selfPath is this piggery executable, symlinks resolved: what harnesses are set up to run.
func selfPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(self)
}

// extensionDir is the directory of a checkout's pi extension given with --ext (the directory or
// its index.ts).
func extensionDir(ext string) (string, error) {
	ext, err := filepath.Abs(ext)
	if err != nil {
		return "", err
	}
	if filepath.Base(ext) == "index.ts" {
		ext = filepath.Dir(ext)
	}
	if _, err := os.Stat(filepath.Join(ext, "index.ts")); err != nil {
		return "", fmt.Errorf("pi extension %s: %w", ext, err)
	}
	return ext, nil
}

// harnessState is where a harness stands, for `piggery setup` and doctor.
type harnessState struct {
	Name      string
	Cmd       string // the harness's command from its profile
	Path      string // where Cmd is on PATH ("" = not found)
	Version   string
	Tested    []string
	Installed bool
	Detail    string
	Problems  []problem
}

// problem is one thing wrong, and the command that fixes it ("" when there is none).
type problem struct{ Text, Fix string }

func (p problem) String() string {
	if p.Fix == "" {
		return p.Text
	}
	return p.Text + "; fix: " + p.Fix
}

func (st harnessState) line() string {
	where := "not on PATH"
	if st.Path != "" {
		where = st.Version
		if where == "" {
			where = "version unknown"
		} else if len(st.Tested) > 0 && !slices.Contains(st.Tested, st.Version) {
			where += fmt.Sprintf(" (not tested; tested: %s)", strings.Join(st.Tested, ", "))
		}
	}
	state := "not installed (piggery setup " + st.Name + ")"
	if st.Installed {
		state = "installed"
		if st.Detail != "" {
			state += ": " + st.Detail
		}
	}
	return fmt.Sprintf("%-7s %-12s %s", st.Name, where, state)
}

// harnessStates checks every setup target; a harness's worker profile may name another cmd and
// the versions piggery was tested with.
func harnessStates(o setupOpts) []harnessState {
	var sts []harnessState
	ints := map[string]integration{}
	for _, i := range installedIntegrations(o.dir) {
		ints[i.Name] = i
	}
	for _, t := range setupTargets {
		sts = append(sts, t.status(o))
		st := &sts[len(sts)-1]
		if i, ok := ints[t.name]; ok && st.Installed && i.outdated() &&
			!slices.ContainsFunc(st.Problems, func(p problem) bool { return strings.HasPrefix(p.Text, "outdated (") }) {
			st.Problems = append(st.Problems, problem{"outdated (" + i.detail() + ")", "piggery setup --outdated"})
		}
		st.Cmd = t.cmd
		var p struct {
			Cmd            string   `json:"cmd"`
			TestedVersions []string `json:"tested_versions"`
		}
		if h, ok := harnessNamed(t.name); ok {
			if b, err := os.ReadFile(h.profilePath(o.dir)); err == nil && json.Unmarshal(b, &p) == nil {
				st.Cmd, st.Tested = firstNonEmpty(p.Cmd, t.cmd), p.TestedVersions
			}
		}
		if path, err := exec.LookPath(st.Cmd); err == nil {
			st.Path = path
			version := local.HarnessVersion
			if t.version != nil {
				version = t.version
			}
			st.Version, _ = version(context.Background(), st.Cmd)
		} else if st.Installed {
			st.Problems = append([]problem{{fmt.Sprintf("`%s` is not on PATH", st.Cmd), "install it, or `piggery setup remove " + st.Name + "`"}}, st.Problems...)
		}
	}
	return sts
}

// harnessWarnings are doctor's lines: problems of an installed harness, and a harness on PATH at
// a version piggery was not tested with (it may misbehave).
func harnessWarnings(dir string) []string {
	self, err := selfPath()
	if err != nil {
		return nil
	}
	var out []string
	for _, st := range harnessStates(setupOpts{dir: dir, self: self}) {
		if st.Path != "" && len(st.Tested) > 0 && !slices.Contains(st.Tested, st.Version) {
			out = append(out, fmt.Sprintf("%s %s is not a tested version (tested: %s); it may misbehave",
				st.Name, firstNonEmpty(st.Version, "(unknown)"), strings.Join(st.Tested, ", ")))
		}
		for _, p := range st.Problems {
			out = append(out, st.Name+": "+p.String())
		}
	}
	return out
}

// writeJSON writes v to path as indented JSON (dirs 0700, file 0600). An existing file is kept
// unless force; it reports whether it wrote.
func writeJSON(path string, v any, force bool) (bool, error) {
	if _, err := os.Stat(path); err == nil && !force {
		return false, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// template new <name> [--from <built-in>]: a copy of the current built-in (default p2p) as a new
// template in ~/.piggery/templates for the user to edit. Local, no daemon; refused if it exists.
func (e *env) template(args []string) error {
	if len(args) == 0 || args[0] != "new" {
		return fmt.Errorf("%w: template new <name> [--from <built-in>]", errUsage)
	}
	fs := e.flags("template new")
	from := fs.String("from", "p2p", "built-in to copy ("+strings.Join(manifests.Builtins(), ", ")+")")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w: template new <name> [--from <built-in>]", errUsage)
	}
	dir, err := manifests.New(e.dir, pos[0], *from)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "wrote %s (from built-in %s); edit %s and its prompts\n", dir, *from, manifests.ManifestFile)
	return nil
}
