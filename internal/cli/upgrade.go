package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/sting8k/piggery/internal/server"
	"github.com/sting8k/piggery/manifests"
)

// What `setup` and `setup --outdated` (and so `update`) say about templates and shared prompts. The
// parts that are piggery's (a template the user never changed, a missing config key, an integration)
// are brought up and only reported. The parts the user changed are theirs: they are listed, and
// `setup --outdated` replaces them only when asked: ticked on a terminal, or named by --replace.

// upgradeItem is a built-in template or a shared prompt the user changed.
type upgradeItem struct {
	Name string // what --replace takes: "slp", "general-policy"
	Note string // what the user changed, in plain words
	rule string // the shared prompt's file ("rules/general-policy.md"); "" for a template
}

// ruleName is the name of a shared prompt file for --replace: rules/general-policy.md -> general-policy.
func ruleName(file string) string { return strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)) }

// upgradeItems are the built-in templates and shared prompts of the home that the user changed.
func (e *env) upgradeItems() ([]upgradeItem, error) {
	differing, err := manifests.Differing(e.dir)
	if err != nil {
		return nil, err
	}
	var items []upgradeItem
	for _, u := range differing {
		note := changeNote(u)
		if len(manifestPins(e.dir, u.Name)) > 0 {
			note += " (your model choice moves to config roles:)"
		}
		items = append(items, upgradeItem{Name: u.Name, Note: note})
	}
	rules, err := manifests.RulesDiffering(e.dir)
	if err != nil {
		return nil, err
	}
	for _, f := range rules {
		items = append(items, upgradeItem{Name: ruleName(f), Note: "you changed " + f, rule: f})
	}
	return items, nil
}

// manifestPins are the deprecated spawn.harness|model|thinking the home's copy of a template sets.
func manifestPins(dir, name string) map[string]map[string]string {
	pins, _ := manifests.SpawnPins(filepath.Join(manifests.Dir(dir), name, manifests.ManifestFile))
	return pins
}

// changeNote is how a changed template reads: "you changed manifest.yaml, prompts/x.md; removed y".
func changeNote(u manifests.Updated) string {
	if u.Yours {
		return "a template of your own with this name"
	}
	by := map[string][]string{}
	for _, d := range u.Diff {
		verb, rel, _ := strings.Cut(d, " ")
		by[verb] = append(by[verb], rel)
	}
	var parts []string
	for _, verb := range []string{"changed", "removed", "added"} {
		files := by[verb]
		if len(files) > 3 {
			parts = append(parts, fmt.Sprintf("%s %d files", verb, len(files)))
		} else if len(files) > 0 {
			parts = append(parts, verb+" "+strings.Join(files, ", "))
		}
	}
	return "you " + strings.Join(parts, "; ")
}

// diffOf is the diff of an item, yours against the version piggery ships.
func (e *env) diffOf(it upgradeItem) ([]diffLine, error) {
	if it.rule != "" {
		p, err := manifests.CompareRule(e.dir, it.rule)
		return diffPairs([]manifests.Pair{p}), err
	}
	pairs, err := manifests.Compare(e.dir, it.Name)
	return diffPairs(pairs), err
}

// parseReplace is the items a --replace value names: "all", "none", or names separated by commas. A
// name that is a built-in template or a shared prompt but was not changed is fine (there is nothing to
// replace); any other name is an error that lists the ones that can be replaced now.
func parseReplace(items []upgradeItem, spec string) ([]upgradeItem, error) {
	switch spec {
	case "all":
		return items, nil
	case "none", "":
		return nil, nil
	}
	var out []upgradeItem
	for _, n := range strings.Split(spec, ",") {
		n = strings.TrimSpace(n)
		if i := slices.IndexFunc(items, func(it upgradeItem) bool { return it.Name == n }); i >= 0 {
			if !slices.ContainsFunc(out, func(it upgradeItem) bool { return it.Name == n }) {
				out = append(out, items[i])
			}
			continue
		}
		known := slices.Contains(manifests.Builtins(), n) || slices.ContainsFunc(manifests.BuiltinPrompts, func(bp manifests.BuiltinPrompt) bool { return ruleName(bp.File) == n })
		if !known {
			names := make([]string, len(items))
			for i, it := range items {
				names[i] = it.Name
			}
			return nil, fmt.Errorf("%w: --replace %q: not a built-in template or shared prompt (changed now: %s)", errUsage, n, cmpList(names))
		}
	}
	return out, nil
}

func cmpList(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// replaceItem replaces one item with piggery's version, keeping the user's copy in a .backup.
func (e *env) replaceItem(it upgradeItem) error {
	if it.rule != "" {
		backup, err := manifests.ReplaceRule(e.dir, it.rule)
		if err != nil {
			return fmt.Errorf("%s: %w", it.Name, err)
		}
		fmt.Fprintf(e.stdout, "✓ %s replaced with the new version; yours is in %s\n", it.Name, backup)
		return nil
	}
	u, err := manifests.Update(e.dir, it.Name, true)
	if err != nil {
		return fmt.Errorf("%s: %w", it.Name, err)
	}
	e.printReplaced(u)
	return nil
}

// upgradeOpts: whether this call may replace what the user changed (setup --outdated) and the --replace value.
type upgradeOpts struct {
	choose     bool
	replace    string
	replaceSet bool
}

// templatesSection is the templates part of setup and setup --outdated, after the home was refreshed:
// what moved on, what the user changed (listed, or chosen and replaced), then the warnings every
// template in the home gets.
func (e *env) templatesSection(unp manifests.Unpacked, o upgradeOpts) error {
	if len(unp.Updated)+len(unp.Added) > 0 {
		var parts []string
		if len(unp.Updated) > 0 {
			parts = append(parts, strings.Join(unp.Updated, ", ")+" updated")
		}
		if len(unp.Added) > 0 {
			parts = append(parts, strings.Join(unp.Added, ", ")+" added")
		}
		fmt.Fprintf(e.stdout, "✓ templates: %s\n", strings.Join(parts, "; "))
	}
	items, err := e.upgradeItems()
	if err != nil {
		return err
	}
	var first error
	switch {
	case len(items) == 0:
	case !o.choose:
		for _, it := range items {
			cmd := "piggery template update " + it.Name + " --force"
			if it.rule != "" {
				cmd = "piggery setup --outdated --replace " + it.Name
			}
			fmt.Fprintf(e.stdout, "  ! %s: %s, so it stays as yours; `%s` replaces it and keeps a copy\n", it.Name, it.Note, cmd)
		}
	default:
		chosen, err := e.chooseItems(items, o)
		if err != nil {
			return err
		}
		for _, it := range chosen {
			if err := e.replaceItem(it); err != nil {
				fmt.Fprintf(e.stdout, "! %v\n", err)
				first = firstErr(first, err)
			}
		}
		var kept []string
		for _, it := range items {
			if !slices.ContainsFunc(chosen, func(c upgradeItem) bool { return c.Name == it.Name }) {
				kept = append(kept, it.Name)
			}
		}
		if len(kept) > 0 {
			fmt.Fprintf(e.stdout, "  kept as yours: %s\n", strings.Join(kept, ", "))
			if !o.replaceSet && e.asker() == nil {
				fmt.Fprintf(e.stdout, "  not a terminal, so nothing was asked; to replace: piggery setup --outdated --replace %s  (yours go to .backup/)\n", strings.Join(kept, ","))
			}
		}
	}
	if err := e.templateWarnings(); err != nil {
		return err
	}
	return first
}

func firstErr(first, err error) error {
	if first == nil {
		return err
	}
	return first
}

// chooseItems is what to replace: the --replace list when given, else the ticks on a terminal, else
// nothing (the caller says what was kept).
func (e *env) chooseItems(items []upgradeItem, o upgradeOpts) ([]upgradeItem, error) {
	if o.replaceSet {
		return parseReplace(items, o.replace)
	}
	ask := e.asker()
	if ask == nil {
		return nil, nil
	}
	head := []string{"You changed " + describeItems(items) + ". Tick the ones to replace with the new version.",
		stMuted.Render("  Replacing keeps your copy in a .backup/ folder next to it.")}
	names, err := ask(head, items)
	if err != nil {
		return nil, err
	}
	var out []upgradeItem
	for _, it := range items {
		if slices.Contains(names, it.Name) {
			out = append(out, it)
		}
	}
	return out, nil
}

// describeItems is "2 built-in templates" or "1 built-in template and the general-policy prompt".
func describeItems(items []upgradeItem) string {
	tpl, rules := 0, 0
	for _, it := range items {
		if it.rule != "" {
			rules++
		} else {
			tpl++
		}
	}
	var parts []string
	switch tpl {
	case 0:
	case 1:
		parts = append(parts, "1 built-in template")
	default:
		parts = append(parts, fmt.Sprintf("%d built-in templates", tpl))
	}
	switch rules {
	case 0:
	case 1:
		parts = append(parts, "a shared prompt")
	default:
		parts = append(parts, fmt.Sprintf("%d shared prompts", rules))
	}
	return strings.Join(parts, " and ")
}

// asker is how to ask for ticks: the test's, or the list on a terminal (stdin and stdout both), else nil.
func (e *env) asker() func(head []string, items []upgradeItem) ([]string, error) {
	if e.ask != nil {
		return e.ask
	}
	out, ok := e.stdout.(*os.File)
	if !ok || e.json || !term.IsTerminal(out.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		return nil
	}
	return func(head []string, items []upgradeItem) ([]string, error) {
		return askTicks(os.Stdin, out, head, items, e.diffOf)
	}
}

// templateWarnings prints what loading every template of the home warns about (a deprecated key
// such as silent_for or a role's spawn.model), one line each, "<template>: <what>". `piggery check`
// prints them with the manifest's path, and the entries to write in config.yaml.
func (e *env) templateWarnings() error {
	errs, warns := checkTemplates(e.dir, false)
	prefix := manifests.Dir(e.dir) + string(filepath.Separator)
	for _, l := range append(errs, warns...) {
		l = strings.TrimPrefix(l, prefix)
		if tpl, what, ok := strings.Cut(l, "/"+manifests.ManifestFile+": "); ok {
			l = tpl + ": " + what
		}
		fmt.Fprintf(e.stdout, "  ! %s\n", l)
	}
	return nil
}

// The reminder: a home where this version has something to do (an integration to update, a template
// or a shared prompt the user changed) is told once per version to run `setup --outdated`.
const setupMarker = ".setup-version"

// markSetup records that this version has been set up (or looked at) in dir.
func markSetup(dir string) {
	_ = os.WriteFile(filepath.Join(dir, setupMarker), []byte(Version+"\n"), 0o600)
}

// needsSetup is whether `setup --outdated` has anything real to do here.
func needsSetup(dir string) bool {
	if len(outdatedIntegrations(dir, true)) > 0 {
		return true
	}
	if d, err := manifests.Differing(dir); err == nil && len(d) > 0 {
		return true
	}
	r, err := manifests.RulesDiffering(dir)
	return err == nil && len(r) > 0
}

// reminder is `piggery <ver>: run piggery setup --outdated` the first time it is asked in a version
// of a home where that command has something to do, else "". Asking records the version.
func (e *env) reminder() string {
	if b, err := os.ReadFile(filepath.Join(e.dir, setupMarker)); err == nil && strings.TrimSpace(string(b)) == Version {
		return ""
	}
	if _, err := os.Stat(e.dir); err != nil { // no home yet: a first install has nothing to bring up
		return ""
	}
	defer markSetup(e.dir)
	if needsSetup(e.dir) {
		return fmt.Sprintf("piggery %s: run piggery setup --outdated", Version)
	}
	return ""
}

// remind prints the reminder after a command that ran on a terminal. Setup and update are what it asks for.
func (e *env) remind(command string) {
	out, ok := e.stdout.(*os.File)
	if !ok || e.json || !term.IsTerminal(out.Fd()) || server.DevBuild(Version) || command == "setup" || command == "update" {
		return
	}
	if line := e.reminder(); line != "" {
		fmt.Fprintln(e.stdout, line)
	}
}
