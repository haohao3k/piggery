package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

// Filled is a config file EnsureFiles changed, and the keys it added.
type Filled struct {
	Path  string
	Added []string
	Kind  string // "": keys added to the file at Path; else what Added says (KindRenamed, KindMoved)
}

const (
	KindRenamed = "renamed" // Path is a manifest in which Added[0] was renamed
	KindMoved   = "moved"   // Path is the backup of the copies; Added are templates whose model choices went into config.yaml roles
)

// EnsureFiles gives every config file piggery owns under dir the keys it lacks, with their
// defaults: config.yaml (EnsureConfig, then the shared prompts piggery ships: UnpackRules), every worker profile (one that is missing is written whole), and every template
// (built-in, edited or the user's; an unedited built-in already has every key). Nothing a file has
// changes; a file its parser refuses is left as it is and named in errs. `piggery setup` and the
// daemon's start run it; the daemon only logs errs.
func EnsureFiles(dir string) (filled []Filled, errs []error) {
	added, manual, err := EnsureConfig(dir)
	switch {
	case err != nil:
		errs = append(errs, err)
	case len(added) > 0:
		filled = append(filled, Filled{Path: ConfigPath(dir), Added: added})
	}
	if len(manual) > 0 {
		errs = append(errs, fmt.Errorf("%s: add %s by hand (gc is not an indented block)", ConfigPath(dir), strings.Join(manual, ", ")))
	}
	// The shared prompts piggery ships: the file, and its config entry once, when it first becomes
	// piggery's; an entry or a file the user removes later stays removed. A role a later piggery
	// adds goes into that entry once; a role the user removes stays removed.
	if err == nil {
		taken, more, err := manifests.UnpackRules(dir)
		if err != nil {
			errs = append(errs, err)
		}
		for _, bp := range taken {
			e := PromptEntry{File: bp.File, Roles: bp.Roles}
			switch added, err := AddPrompt(dir, e); {
			case err != nil:
				errs = append(errs, err)
			case !added:
				errs = append(errs, fmt.Errorf("%s: add to prompts by hand: {file: %s, roles: [%s]}", ConfigPath(dir), e.File, strings.Join(e.Roles, ", ")))
			default:
				filled = append(filled, Filled{Path: ConfigPath(dir), Added: []string{"prompts: " + e.File + " for " + strings.Join(e.Roles, ", ")}})
			}
		}
		// A role a newer piggery offers the prompt to goes into the user's entry once.
		for _, bp := range more {
			switch added, byHand, err := AddPromptRoles(dir, bp.File, bp.Roles); {
			case err != nil:
				errs = append(errs, err)
			case byHand:
				errs = append(errs, fmt.Errorf("%s: add %s to the roles of the prompts entry for %s by hand", ConfigPath(dir), strings.Join(bp.Roles, ", "), bp.File))
			case len(added) > 0:
				filled = append(filled, Filled{Path: ConfigPath(dir), Added: []string{"prompts: " + bp.File + " also for " + strings.Join(added, ", ")}})
			}
		}
	}
	for _, p := range local.DefaultProfiles(dir) {
		if added, err := local.FillProfile(p.Path, p.Default); err != nil {
			errs = append(errs, err)
		} else if len(added) > 0 {
			filled = append(filled, Filled{Path: p.Path, Added: added})
		}
	}
	// A template that still names itself with `model:` gets `template:` in its place, before its keys are filled.
	if renamed, err := manifests.MigrateKey(dir); err != nil {
		errs = append(errs, err)
	} else if len(renamed) > 0 {
		for _, p := range renamed {
			filled = append(filled, Filled{Path: p, Added: []string{"template (was model)"}})
		}
	}
	// A timer rule's old key silent_for becomes working_for; the old name stays readable for a manifest
	// outside the home and for the one a running team stored.
	if renamed, err := manifests.MigrateTimers(dir); err != nil {
		errs = append(errs, err)
	} else {
		for _, p := range renamed {
			filled = append(filled, Filled{Path: p, Added: []string{"silent_for renamed working_for"}, Kind: KindRenamed})
		}
	}
	paths, _ := filepath.Glob(filepath.Join(manifests.Dir(dir), "*", manifests.ManifestFile))
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if added, err := core.FillManifestFile(p); err != nil {
			errs = append(errs, err)
		} else if len(added) > 0 {
			filled = append(filled, Filled{Path: p, Added: added})
		}
	}
	return filled, errs
}
