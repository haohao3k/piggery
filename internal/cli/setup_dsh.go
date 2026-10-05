package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sting8k/piggery/internal/driver/local"
)

// dsh (DeepSeek Harness): piggery's plugin is the copy the binary carries, unpacked under
// ~/.piggery/plugins/dsh, plus one managed block in dsh's home patch ($DSH_HOME/cordis.patch.yml, default
// ~/.dsh), the layer every dsh profile composes last-but-one, which inserts the plugin's entry as a
// row. No pnpm and no profile edit: `dsh web` (and any profile) loads it from the next start. The
// user's own rows in the file stay as they are. The dsh worker profile (~/.piggery/harness/dsh.json)
// is written by `piggery setup` and the daemon; workers load the same plugin through their own
// --patch overlay (local.EnsureDshWorker), which needs no setup dsh.

// dshHarness: dsh's plugin talks to the daemon itself (no hooks, no piggery mcp).
var dshHarness = harnessProfile{
	setupTarget: setupTarget{name: "dsh", cmd: "dsh",
		install: func(o setupOpts) (string, error) { return installDsh(o.dir) },
		refresh: func(o setupOpts) (string, error) { return refreshDsh(o) },
		remove:  func(o setupOpts) (string, error) { return removeDsh(o.dir) },
		status:  func(o setupOpts) harnessState { return dshStatus(o.dir, o.self) },
		version: func(ctx context.Context, cmd string) (string, error) { return local.DshVersion(ctx, cmd) },
	},
	profilePath: local.DshProfilePath,
}

// dshHomePatch is the home patch file: $DSH_HOME (blank is unset) or ~/.dsh.
func dshHomePatch() string {
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		u, _ := os.UserHomeDir()
		home = filepath.Join(u, ".dsh")
	} else if home == "~" || strings.HasPrefix(home, "~/") {
		u, _ := os.UserHomeDir()
		home = filepath.Join(u, strings.TrimPrefix(home, "~"))
	}
	return filepath.Join(home, "cordis.patch.yml")
}

const (
	dshBegin = "# BEGIN piggery: managed by `piggery setup dsh`; `piggery setup remove dsh` takes it out"
	dshEnd   = "# END piggery"
)

// dshBlock is the managed block for the piggery dir: one row inserting the plugin's entry, with the
// directory the plugin keeps its session records in (config.sessions: the plugin takes its
// paths from piggery, it knows none).
func dshBlock(dir string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return dshBegin + "\n- insert:\n    - id: piggery\n      name: " + q(local.DshEntry(dir)) +
		"\n      config:\n        sessions: " + q(local.DshSessionsDir(dir)) + "\n" + dshEnd + "\n"
}

// blockSpan is where the managed block is in doc (its begin line to the end of its end line).
func blockSpan(doc string) (start, end int, ok bool) {
	start = strings.Index(doc, dshBegin)
	if start < 0 || (start > 0 && doc[start-1] != '\n') {
		return 0, 0, false
	}
	e := strings.Index(doc[start:], dshEnd)
	if e < 0 {
		return 0, 0, false
	}
	end = start + e + len(dshEnd)
	if end < len(doc) && doc[end] == '\n' {
		end++
	}
	return start, end, true
}

// validPatch: doc is a YAML list (or empty), what dsh reads a patch file as.
func validPatch(doc string) error {
	var rows []any
	if err := yaml.Unmarshal([]byte(doc), &rows); err != nil {
		return err
	}
	return nil
}

// withBlock is doc with the managed block for dir: replaced where it is, else added at the end.
// A file that does not read as a list once it has the block (a flow `[...]` with rows, a mapping) is
// refused, never rewritten.
func withBlock(doc, dir string) (string, error) {
	block := dshBlock(dir)
	var out string
	switch start, end, ok := blockSpan(doc); {
	case ok:
		out = doc[:start] + block + doc[end:]
	case strings.TrimSpace(doc) == "[]":
		out = block // `[]`: an empty list, spelled flow
	default:
		out = doc
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += block
	}
	if err := validPatch(out); err != nil {
		return "", fmt.Errorf("its rows would not read as a YAML list (%v): add the block by hand:\n%s", err, block)
	}
	return out, nil
}

// withoutBlock is doc without the managed block (removed reports whether there was one).
func withoutBlock(doc string) (out string, removed bool) {
	start, end, ok := blockSpan(doc)
	if !ok {
		return doc, false
	}
	return doc[:start] + doc[end:], true
}

// writeAtomic writes doc to path (dirs 0700, an existing file's mode kept, new one 0600).
func writeAtomic(path, doc string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".piggery-tmp"
	if err := os.WriteFile(tmp, []byte(doc), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// refreshDsh updates the managed plugin only while dsh's setup-owned row is still present. A
// removed or edited row is a disabled/unregistered host state; restoring it belongs to setup dsh,
// not setup --refresh.
func refreshDsh(o setupOpts) (string, error) {
	ext := local.DshExtDir(o.dir)
	if _, managed := local.DshExtVersion(ext); !managed {
		return "dsh: skipped (the managed plugin is not installed)", nil
	}
	patch := dshHomePatch()
	cur, err := os.ReadFile(patch)
	if errors.Is(err, os.ErrNotExist) {
		return "dsh: skipped (dsh's piggery row is not registered)", nil
	}
	if err != nil {
		return "", err
	}
	if !bytes.Contains(cur, []byte(dshBlock(o.dir))) {
		return "dsh: skipped (dsh's piggery row is not registered)", nil
	}
	wrote, err := local.InstallDshExt(ext)
	if err != nil {
		return "", err
	}
	if !wrote {
		return "dsh: already current", nil
	}
	return fmt.Sprintf("dsh: refreshed piggery's plugin (v%d) in %s", local.IntegrationVersion("dsh"), ext), nil
}

func installDsh(dir string) (string, error) {
	ext, patch := local.DshExtDir(dir), dshHomePatch()
	cur, err := os.ReadFile(patch)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	doc, err := withBlock(string(cur), dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", patch, err)
	}
	wrote, err := local.InstallDshExt(ext)
	if err != nil {
		return "", err
	}
	changed := doc != string(cur)
	backup := ""
	if changed {
		if backup, err = backupHumanConfig(dir, "dsh", patch, func(b []byte) bool { _, _, ok := blockSpan(string(b)); return ok }); err != nil {
			return "", err
		}
		if err := writeAtomic(patch, doc); err != nil {
			return "", err
		}
	}
	if !wrote && !changed {
		return "dsh: piggery's plugin is already installed", nil
	}
	return fmt.Sprintf("dsh: installed piggery's plugin (v%d) in %s and its row in %s\n%sdsh: sessions started from now on join piggery; restart a dsh that is open.", local.IntegrationVersion("dsh"), ext, patch, backupLines([]string{backup})), nil
}

func removeDsh(dir string) (string, error) {
	patch := dshHomePatch()
	removed := false
	if cur, err := os.ReadFile(patch); err == nil {
		if doc, ok := withoutBlock(string(cur)); ok {
			removed = true
			if strings.TrimSpace(doc) == "" {
				err = os.Remove(patch) // nothing but piggery's block was in it
			} else {
				err = writeAtomic(patch, doc)
			}
			if err != nil {
				return "", err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	ext := local.DshExtDir(dir)
	if had, err := local.RemoveDshExt(ext); err != nil {
		return "", err
	} else if had {
		removed = true
	}
	if !removed {
		return "dsh: piggery is not installed", nil
	}
	return "dsh: removed " + ext + " and piggery's row in " + patch, nil
}

// dshStatus: the installed copy, its row in the home patch naming that copy's entry, and `piggery`
// on PATH being this binary (the plugin starts the daemon with it).
func dshStatus(dir, self string) harnessState {
	st := harnessState{Name: "dsh"}
	ext := local.DshExtDir(dir)
	have, managed := local.DshExtVersion(ext)
	if !managed {
		if _, err := os.Lstat(ext); err == nil {
			st.Problems = append(st.Problems, problem{ext + " is not piggery's (no \"managed by piggery\" line): setup dsh will not replace it", "move it away, then `piggery setup dsh`"})
		}
		return st
	}
	st.Installed, st.Detail = true, fmt.Sprintf("%s (v%d)", ext, have)
	patch := dshHomePatch()
	cur, _ := os.ReadFile(patch)
	if !bytes.Contains(cur, []byte(dshBlock(dir))) {
		st.Problems = append(st.Problems, problem{"dsh's home patch " + patch + " has no row for piggery's plugin (or one for another copy), so dsh does not load it", "piggery setup dsh"})
	}
	st.Problems = append(st.Problems, piggeryOnPath(self)...)
	return st
}
