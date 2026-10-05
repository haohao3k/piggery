package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/internal/jsonobj"
)

// opencode: piggery's plugin is the copy the binary carries, unpacked into ~/.piggery/plugins/opencode,
// plus one entry in `plugin` of opencode's user config (opencode.json in $XDG_CONFIG_HOME/opencode or
// ~/.config/opencode): a plugins directory in opencode's config dir would make opencode install npm
// packages there. The Human's other entries and every other key stay as they are. A config that is
// not plain JSON (opencode.jsonc, comments) is never rewritten: setup writes nothing and prints the
// line to add. Workers load the same copy through OPENCODE_CONFIG_CONTENT (local.opencode.go) and
// need no setup. The opencode worker profile (~/.piggery/harness/opencode.json) is written by
// `piggery setup` and the daemon.

// opencodeHarness: opencode's plugin talks to the daemon itself (no hooks, no piggery mcp).
var opencodeHarness = harnessProfile{
	setupTarget: setupTarget{name: "opencode", cmd: "opencode",
		install: func(o setupOpts) (string, error) { return installOpencode(o.dir) },
		remove:  func(o setupOpts) (string, error) { return removeOpencode(o.dir) },
		status:  func(o setupOpts) harnessState { return opencodeStatus(o.dir, o.self) },
	},
	profilePath: local.OpencodeProfilePath,
}

// opencodeConfigDir is opencode's user config directory.
func opencodeConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "opencode")
}

// opencodeConfig is the user config setup edits: opencode.json, and whether opencode.jsonc stands
// in its place (it is never edited).
func opencodeConfig() (path string, jsonc bool) {
	dir := opencodeConfigDir()
	if _, err := os.Stat(filepath.Join(dir, "opencode.json")); err != nil {
		if _, err := os.Stat(filepath.Join(dir, "opencode.jsonc")); err == nil {
			return filepath.Join(dir, "opencode.jsonc"), true
		}
	}
	return filepath.Join(dir, "opencode.json"), false
}

// opencodeSpec is a `plugin` entry's spec: the string, or the first item of a [spec, options] pair.
func opencodeSpec(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var pair []json.RawMessage
	if json.Unmarshal(raw, &pair) == nil && len(pair) > 0 && json.Unmarshal(pair[0], &s) == nil {
		return s
	}
	return ""
}

// opencodePlugins reads the config at path: its object and `plugin` entries. A missing file is an
// empty object; one that is not plain JSON is the error.
func opencodePlugins(path string) (jsonobj.Object, []json.RawMessage, error) {
	b, err := readOptional(path)
	if err != nil {
		return nil, nil, err
	}
	o, err := jsonobj.Parse(b)
	if err != nil {
		return nil, nil, err
	}
	var entries []json.RawMessage
	if raw, ok := o.Get("plugin"); ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, nil, fmt.Errorf("plugin: %w", err)
		}
	}
	return o, entries, nil
}

// refuseOpencodeConfig is what setup says instead of editing a config it cannot rewrite.
func refuseOpencodeConfig(path string, why error, entry string) error {
	spec := local.OpencodePluginSpec(entry)
	return fmt.Errorf("opencode: %s is not plain JSON (%v), so setup does not rewrite it and wrote nothing.\n"+
		"Add this line to its \"plugin\" array yourself, then run `piggery setup opencode` again:\n  %s", path, why, jsonobj.String(spec))
}

func installOpencode(dir string) (string, error) {
	cfgPath, jsonc := opencodeConfig()
	entry := local.OpencodeEntry(dir)
	spec := local.OpencodePluginSpec(entry)
	if jsonc {
		return "", refuseOpencodeConfig(cfgPath, errors.New("a .jsonc file"), entry)
	}
	o, _, err := opencodePlugins(cfgPath)
	if err != nil {
		return "", refuseOpencodeConfig(cfgPath, err, entry)
	}
	wrote, err := local.InstallOpencodeExt(local.OpencodeExtDir(dir))
	if err != nil {
		return "", err
	}
	var msgs []string
	if wrote {
		msgs = append(msgs, fmt.Sprintf("opencode: installed piggery's plugin (v%d) in %s", local.IntegrationVersion("opencode"), local.OpencodeExtDir(dir)))
	}
	// The `plugin` array is edited in place: the Human's entries keep their text and layout.
	rawPlugin, hadPlugin := o.Get("plugin")
	placed, changed := false, false
	if hadPlugin {
		if rawPlugin, err = jsonobj.ArrayRemove(rawPlugin, func(item json.RawMessage) bool {
			s := opencodeSpec(item)
			if _, mine := local.OpencodeEntryOf(s); !mine {
				return false
			}
			if s == spec && !placed {
				placed = true
				return false
			}
			changed = true // another copy's entry, or a second one
			return true
		}); err != nil {
			return "", refuseOpencodeConfig(cfgPath, err, entry)
		}
	}
	if !placed {
		changed = true
		if hadPlugin {
			rawPlugin, err = jsonobj.ArrayAppend(rawPlugin, jsonobj.String(spec))
		} else {
			rawPlugin = rawArray([]json.RawMessage{jsonobj.String(spec)})
		}
		if err != nil {
			return "", refuseOpencodeConfig(cfgPath, err, entry)
		}
	}
	if changed {
		backup, err := backupHumanConfig(dir, "opencode", cfgPath, func(b []byte) bool { return hasOpencodeEntry(b) })
		if err != nil {
			return "", err
		}
		if backup != "" {
			msgs = append(msgs, backup)
		}
		if hadPlugin {
			o = o.SetVerbatim("plugin", rawPlugin)
		} else {
			o = o.Set("plugin", rawPlugin)
		}
		if err := writeOpencodeConfig(cfgPath, o); err != nil {
			return "", err
		}
		msgs = append(msgs, fmt.Sprintf("opencode: added %s to \"plugin\" in %s", spec, cfgPath))
	}
	if len(msgs) == 0 {
		return "opencode: piggery's plugin is already installed", nil
	}
	return strings.Join(msgs, "\n") + "\nopencode: sessions started from now on join piggery; restart any that are open.", nil
}

// writeOpencodeConfig writes o to path; the file keeps its final newline or its lack of one.
func writeOpencodeConfig(path string, o jsonobj.Object) error {
	b := o.Bytes(0)
	if old, _ := readOptional(path); len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		b = bytes.TrimSuffix(b, []byte("\n"))
	}
	return writeFileAtomic(path, b)
}

// hasOpencodeEntry: the config b lists piggery's plugin.
func hasOpencodeEntry(b []byte) bool {
	o, err := jsonobj.Parse(b)
	if err != nil {
		return false
	}
	raw, _ := o.Get("plugin")
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return false
	}
	for _, e := range entries {
		if _, mine := local.OpencodeEntryOf(opencodeSpec(e)); mine {
			return true
		}
	}
	return false
}

func removeOpencode(dir string) (string, error) {
	var msgs []string
	cfgPath, jsonc := opencodeConfig()
	entry := local.OpencodeEntry(dir)
	o, _, err := opencodePlugins(cfgPath)
	if jsonc {
		err = errors.New("a .jsonc file")
	}
	if err != nil {
		if _, serr := os.Stat(local.OpencodeExtDir(dir)); serr != nil {
			return "opencode: piggery is not installed", nil
		}
		return "", fmt.Errorf("opencode: %s is not plain JSON (%v), so setup cannot take piggery's entry out of it and left the plugin in place.\n"+
			"Remove its line from the \"plugin\" array yourself (%s), then run `piggery setup remove opencode` again", cfgPath, err, jsonobj.String(local.OpencodePluginSpec(entry)))
	}
	raw, _ := o.Get("plugin")
	dropped := false
	kept, aerr := jsonobj.ArrayRemove(raw, func(item json.RawMessage) bool {
		_, mine := local.OpencodeEntryOf(opencodeSpec(item))
		dropped = dropped || mine
		return mine
	})
	if aerr != nil {
		return "", aerr
	}
	if dropped {
		var left []json.RawMessage
		json.Unmarshal(kept, &left)
		switch {
		case len(left) > 0:
			o = o.SetVerbatim("plugin", kept)
		default:
			o = o.Del("plugin")
		}
		if len(o) == 0 { // as for pi, codex and dsh: a config left as {} goes
			if err := os.Remove(cfgPath); err != nil {
				return "", err
			}
		} else if err := writeOpencodeConfig(cfgPath, o); err != nil {
			return "", err
		}
		msgs = append(msgs, "opencode: removed piggery's plugin from "+cfgPath)
	}
	if removed, err := local.RemoveOpencodeExt(local.OpencodeExtDir(dir)); err != nil {
		return "", err
	} else if removed {
		msgs = append(msgs, "opencode: removed "+local.OpencodeExtDir(dir))
	}
	if len(msgs) == 0 {
		return "opencode: piggery is not installed", nil
	}
	return strings.Join(msgs, "\n"), nil
}

// opencodeStatus: the installed copy, its entry in opencode's config naming that copy, and
// `piggery` on PATH being this binary (the plugin starts the daemon with it).
func opencodeStatus(dir, self string) harnessState {
	st := harnessState{Name: "opencode"}
	ext := local.OpencodeExtDir(dir)
	have, managed := local.OpencodeExtVersion(ext)
	if !managed {
		if _, err := os.Lstat(ext); err == nil {
			st.Problems = append(st.Problems, problem{ext + " is not piggery's (no \"managed by piggery\" line): setup opencode will not replace it", "move it away, then `piggery setup opencode`"})
		}
		return st
	}
	st.Installed, st.Detail = true, fmt.Sprintf("%s (v%d)", ext, have)
	cfgPath, jsonc := opencodeConfig()
	spec := local.OpencodePluginSpec(local.OpencodeEntry(dir))
	if _, entries, err := opencodePlugins(cfgPath); jsonc || err != nil {
		st.Problems = append(st.Problems, problem{cfgPath + " is not plain JSON: setup cannot tell whether it loads piggery's plugin (it needs the line " + string(jsonobj.String(spec)) + " in \"plugin\")", ""})
	} else if !containsSpec(entries, spec) {
		st.Problems = append(st.Problems, problem{"opencode's config " + cfgPath + " has no entry for piggery's plugin (or one for another copy), so opencode does not load it", "piggery setup opencode"})
	}
	st.Problems = append(st.Problems, piggeryOnPath(self)...)
	return st
}

func containsSpec(entries []json.RawMessage, spec string) bool {
	for _, e := range entries {
		if opencodeSpec(e) == spec {
			return true
		}
	}
	return false
}
