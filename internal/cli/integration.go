package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/internal/proto"
)

// Integration versions (local/integration.go): each thing piggery installs for a harness has an
// integer, and what is installed carries the one it was written with. An integration is outdated
// when that integer is lower than this binary's, or, for Codex, when a hook piggery writes is
// missing from hooks.json. The check reads a few small files and starts no process, so ps and
// top can run it on every refresh.

// integration is one installed part of piggery against this binary's integer.
type integration struct {
	Name       string
	Have, Want int    // what is installed (0: from before the integers), what this binary writes
	Drift      string // why the entries differ from what this binary writes at the same integer ("" = they do not)
}

func (i integration) outdated() bool { return i.Have < i.Want || i.Drift != "" }

func (i integration) proto() proto.Outdated {
	return proto.Outdated{Name: i.Name, Have: i.Have, Want: i.Want, Drift: i.Drift}
}

// detail is `v1 < v2`, or `v2: <drift>` when the integer is the binary's but the entries differ.
func (i integration) detail() string { return i.proto().Detail() }

// manualIntegrations are the ones the daemon does not bring up by itself (`piggery setup
// --outdated` does): their files sit in the harness's own config or in an app that has to reload
// them. The others (pi, omp, dsh, opencode) are brought up by the daemon at its start.
var manualIntegrations = map[string]bool{"claude": true, "codex": true, "paseo": true}

// installedIntegrations is every integration of dir (and the Codex home) that is installed, with
// its state, in the order of setupTargets.
func installedIntegrations(dir string) []integration {
	var out []integration
	add := func(name string, have int, installed bool, drift string) {
		if installed {
			out = append(out, integration{Name: name, Have: have, Want: local.IntegrationVersion(name), Drift: drift})
		}
	}
	for _, t := range setupTargets {
		switch t.name {
		case "pi":
			have, managed := local.PiExtVersion(local.PiExtDir(dir))
			add("pi", have, managed, "")
		case "omp":
			have, managed := local.OmpExtVersion(local.OmpExtDir(dir))
			add("omp", have, managed, "")
		case "dsh":
			have, managed := local.DshExtVersion(local.DshExtDir(dir))
			add("dsh", have, managed, "")
		case "opencode":
			have, managed := local.OpencodeExtVersion(local.OpencodeExtDir(dir))
			add("opencode", have, managed, "")
		case "claude":
			have, installed := claudeIntegration(filepath.Join(dir, "claude"))
			add("claude", have, installed, "")
		case "codex":
			have, installed, drift := codexIntegration(codexHome())
			add("codex", have, installed, drift)
		case "paseo":
			have, installed := paseoIntegration(paseoDir(dir))
			add("paseo", have, installed, "")
		}
	}
	return out
}

// outdatedIntegrations are the installed integrations older than this binary's, with the command
// that brings each up: the manual ones only, when manual is set.
func outdatedIntegrations(dir string, manual bool) (outdated []integration) {
	for _, i := range installedIntegrations(dir) {
		if i.outdated() && (!manual || manualIntegrations[i.Name]) {
			outdated = append(outdated, i)
		}
	}
	return outdated
}

// DaemonOutdated is server.Config.Integrations: the manual integrations that are outdated. The
// daemon logs them at its start and ps reports them on each call, so ps, top and the Paseo plugin
// share one list.
func DaemonOutdated(dir string) []proto.Outdated {
	var out []proto.Outdated
	for _, i := range outdatedIntegrations(dir, true) {
		out = append(out, i.proto())
	}
	return out
}

// claudeVersionString is the version plugin.json carries for integer n (Claude wants a version
// that looks like a semver, and installs the plugin again when it changes).
func claudeVersionString(n int) string { return "0.0." + strconv.Itoa(n) }

// claudePluginVersion is the integer in the plugin.json of the plugin at dir (0 for a build
// version from before the integers); ok is false when there is no plugin.json to read.
func claudePluginVersion(dir string) (n int, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return 0, false
	}
	var p struct{ Version string }
	if json.Unmarshal(b, &p) != nil {
		return 0, true
	}
	if v, found := strings.CutPrefix(p.Version, "0.0."); found {
		n, _ = strconv.Atoi(v)
	}
	return n, true
}

// claudeIntegration: whether Claude has piggery (as setup's status asks it: the user MCP server, the
// marketplace or the plugin, read from Claude's own files, no process), and the integer of the plugin
// it runs (its copy, else what setup wrote under root). Files left in root with nothing in Claude are
// not an install.
func claudeIntegration(root string) (have int, installed bool) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		pluginsDir = filepath.Join(d, "plugins")
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if b, _ := readOptional(filepath.Join(home, ".claude.json")); len(b) > 0 {
		_ = json.Unmarshal(b, &cfg)
	}
	var plugins struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if b, _ := readOptional(filepath.Join(pluginsDir, "installed_plugins.json")); len(b) > 0 {
		_ = json.Unmarshal(b, &plugins)
	}
	var marketplaces map[string]json.RawMessage
	if b, _ := readOptional(filepath.Join(pluginsDir, "known_marketplaces.json")); len(b) > 0 {
		_ = json.Unmarshal(b, &marketplaces)
	}
	_, mcp := cfg.MCPServers["piggery"]
	copies, plugin := plugins.Plugins[local.ClaudePlugin]
	_, market := marketplaces[claudeMarketplace]
	if !mcp && !plugin && !market {
		return 0, false
	}
	dir := filepath.Join(root, "piggery")
	if len(copies) > 0 && copies[0].InstallPath != "" {
		dir = copies[0].InstallPath
	}
	have, _ = claudePluginVersion(dir)
	return have, true
}

// codexMarker is the line after the block's first line that carries the integer.
func codexMarker(n int) string { return "# " + local.IntegrationMarker + strconv.Itoa(n) }

// codexIntegration: piggery's block in config.toml and its hook groups in hooks.json. Drift is
// the hooks that are not all there (a hook added to piggery since).
func codexIntegration(home string) (have int, installed bool, drift string) {
	cfg, _ := readOptional(filepath.Join(home, "config.toml"))
	_, rest, block := strings.Cut(string(cfg), codexBlockBegin+"\n")
	hooks, _ := readOptional(filepath.Join(home, "hooks.json"))
	n := 0
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if len(hooks) > 0 && json.Unmarshal(hooks, &doc) == nil {
		for _, ev := range codexHookEvents {
			for _, g := range doc.Hooks[ev.name] {
				if isPiggeryCodexGroup(g) {
					n++
					break
				}
			}
		}
	}
	if !block && n == 0 {
		return 0, false, ""
	}
	if line, _, _ := strings.Cut(rest, "\n"); block && strings.HasPrefix(line, "# "+local.IntegrationMarker) {
		have = local.ParseIntegration(strings.TrimPrefix(line, "# "))
	}
	if n < len(codexHookEvents) {
		drift = fmt.Sprintf("%d of %d hooks are in hooks.json", n, len(codexHookEvents))
	}
	return have, true, drift
}

// paseoVersionFile is the content of the VERSION file of the plugin copy: the marker and the
// integer. The file also says the directory is piggery's.
func paseoVersionFile() []byte {
	return []byte(local.IntegrationMarker + strconv.Itoa(local.IntegrationVersion("paseo")) + "\n")
}

// paseoIntegration: the plugin copy setup wrote to dst (~/.piggery/paseo).
func paseoIntegration(dst string) (have int, installed bool) {
	b, err := os.ReadFile(filepath.Join(dst, "VERSION"))
	if err != nil {
		return 0, false
	}
	return local.ParseIntegration(string(bytes.TrimSpace(b))), true
}
