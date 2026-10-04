package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/internal/server"
	"github.com/sting8k/piggery/manifests"
)

// checkReport is what `piggery check` found: what team up or serve would refuse, then what they
// would skip or ignore.
type checkReport struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// checkFiles is `check` over the piggery dir: a caller of the loaders that already run (the config
// loader and its warnings, CheckPrompts, the profile loaders, core.CheckManifest for every
// template); it has no rule of its own and writes nothing (EnsureFiles, which does, is not called).
func checkFiles(dir string) checkReport {
	r := checkReport{Errors: []string{}, Warnings: []string{}}
	set, err := server.LoadSettings(dir)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
	} else {
		r.Warnings = append(r.Warnings, set.Warnings...)
		if _, w := server.ColumnsOf(set, dir); w != "" {
			r.Warnings = append(r.Warnings, w)
		}
		_, warns := server.CheckPrompts(dir, set.Prompts)
		r.Warnings = append(r.Warnings, warns...)
	}
	if _, legacy := server.NotifyHooks(dir); legacy {
		r.Warnings = append(r.Warnings, filepath.Join(dir, "hooks", "notify")+" is no longer run: move it into "+server.NotifyHooksDir(dir)+"/")
	}
	for _, err := range local.CheckProfiles(dir) {
		r.Errors = append(r.Errors, err.Error())
	}
	templates := map[string]string{} // manifest path -> the self-contained manifest
	if listed, err := manifests.List(dir); err != nil {
		r.Errors = append(r.Errors, err.Error())
	} else {
		for _, l := range listed {
			path := filepath.Join(l.From, manifests.ManifestFile)
			text, err := manifests.Resolve(l.Name, dir)
			if err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			templates[path] = text
		}
		for _, name := range manifests.Builtins() { // a built-in setup has not unpacked yet
			if slices.ContainsFunc(listed, func(l manifests.Listed) bool { return l.Name == name }) {
				continue
			}
			if text, err := manifests.Builtin(name); err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("built-in template %s: %v", name, err))
			} else {
				templates["built-in template "+name] = text
			}
		}
	}
	paths := make([]string, 0, len(templates))
	for p := range templates {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		warns, err := core.CheckManifest(templates[p])
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", p, err))
		}
		for _, w := range warns {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: %s", p, w))
		}
	}
	return r
}

// check is `piggery check`: local, no daemon. Like doctor: the errors, then `warning:` lines; exit 1
// on any error.
func (e *env) check(args []string) error {
	pos, err := parse(e.flags("check"), args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("%w: check takes no arguments", errUsage)
	}
	r := checkFiles(e.dir)
	if e.json {
		b, _ := json.Marshal(r)
		fmt.Fprintln(e.stdout, string(b))
	} else {
		for _, l := range r.Errors {
			fmt.Fprintln(e.stdout, l)
		}
		for _, w := range r.Warnings {
			fmt.Fprintln(e.stdout, "warning: "+w)
		}
		if len(r.Errors)+len(r.Warnings) == 0 {
			fmt.Fprintln(e.stdout, "no problems")
		}
	}
	if len(r.Errors) > 0 {
		return errFindings
	}
	return nil
}
