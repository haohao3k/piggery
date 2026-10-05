package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

func TestSetupRefreshRepairsSameVersionAssetAndKeepsUninstalledState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".piggery")

	if _, err := installPi(dir, ""); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(local.PiExtDir(dir), "index.ts")
	b, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, append(b, []byte("// local fork asset\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if have, managed := local.PiExtVersion(local.PiExtDir(dir)); !managed || have != local.IntegrationVersion("pi") {
		t.Fatalf("corrupting bytes changed the marker: v%d managed=%v", have, managed)
	}

	profile := local.ProfilePath(dir)
	profileBytes := []byte(`{"cmd":"mine","args":["--kept"]}`)
	if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, profileBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// These are user-owned or uninstalled target state. Refresh must not create or rewrite them.
	claudeRoot := filepath.Join(dir, "claude")
	if err := os.MkdirAll(claudeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	claudeSentinel := filepath.Join(claudeRoot, "keep.txt")
	if err := os.WriteFile(claudeSentinel, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexConfig, []byte("model = \"mine\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paseoSentinel := filepath.Join(dir, "paseo", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(paseoSentinel), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paseoSentinel, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err = (&env{dir: dir, stdout: &out}).setup([]string{"--refresh", "--force"})
	if err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	if !local.PiExtCurrent(local.PiExtDir(dir)) {
		t.Fatal("refresh left the same-version pi asset stale")
	}
	if got, err := os.ReadFile(profile); err != nil || string(got) != string(profileBytes) {
		t.Fatalf("refresh changed profile: %q, %v", got, err)
	}
	if got, err := os.ReadFile(claudeSentinel); err != nil || string(got) != "mine" {
		t.Fatalf("refresh changed uninstalled Claude state: %q, %v", got, err)
	}
	if got, err := os.ReadFile(codexConfig); err != nil || string(got) != "model = \"mine\"\n" {
		t.Fatalf("refresh changed uninstalled Codex state: %q, %v", got, err)
	}
	if got, err := os.ReadFile(paseoSentinel); err != nil || string(got) != "mine" {
		t.Fatalf("refresh changed uninstalled Paseo state: %q, %v", got, err)
	}
	if !strings.Contains(out.String(), "templates refreshed") || !strings.Contains(out.String(), "pi: refreshed") {
		t.Fatalf("refresh output: %q", out.String())
	}
}

func TestSetupRefreshRejectsOutdatedAndHarnessModes(t *testing.T) {
	e := &env{dir: t.TempDir(), stdout: &strings.Builder{}}
	for _, args := range [][]string{{"--refresh", "--outdated"}, {"--refresh", "pi"}, {"--refresh", "--ext", "."}} {
		if err := e.setup(args); err == nil || !strings.Contains(err.Error(), "setup --refresh") {
			t.Fatalf("setup %v error = %v", args, err)
		}
	}
}

func TestSetupRefreshReportsPreservedTemplateOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".piggery")
	if err := manifests.Unpack(dir); err != nil {
		t.Fatal(err)
	}
	name := manifests.Builtins()[0]
	path := filepath.Join(manifests.Dir(dir), name, manifests.ManifestFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, []byte("# local override\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := (&env{dir: dir, stdout: &out}).setup([]string{"--refresh"}); err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	if want := "preserved local overrides: " + name; !strings.Contains(out.String(), want) {
		t.Fatalf("refresh output %q, want %q", out.String(), want)
	}
}

func TestSetupRefreshReinstallsSameVersionClaudeCache(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PATH", bin)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "claude-argv")
	t.Setenv("PIGGERY_FAKE_CLAUDE", log)
	dir := filepath.Join(home, ".piggery")
	self, err := selfPath()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "claude")
	if _, err := installClaude(filepath.Dir(root), self); err != nil {
		t.Fatal(err)
	}
	changes(t, log)

	cache := filepath.Join(home, ".claude", "plugins", "cache", "piggery", "piggery")
	hooks := filepath.Join(cache, "hooks", "hooks.json")
	b, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, append(b, []byte("\n// stale same-version cache\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := (&env{dir: dir, stdout: &out}).setup([]string{"--refresh"}); err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	if got := changes(t, log); !slices.Equal(got, []string{
		"plugin uninstall piggery@piggery",
		"plugin install piggery@piggery",
	}) {
		t.Fatalf("refresh commands: %q", got)
	}
	if !claudePluginCurrent(filepath.Join(root, "piggery"), cache) {
		t.Fatal("refresh left Claude's same-version cache stale")
	}
	if !strings.Contains(out.String(), "claude: refreshed the cached piggery plugin") {
		t.Fatalf("refresh output: %q", out.String())
	}
	b, err = os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, append(b, []byte("\n// stale again\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIGGERY_FAKE_CLAUDE_NO_CACHE_REFRESH", "1")
	if _, err := refreshClaude(root, self); err == nil || !strings.Contains(err.Error(), "cache is still stale") {
		t.Fatalf("refresh with unchanged cache error = %v", err)
	}
}

func TestSetupRefreshDoesNotEnableDisabledPaseo(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PATH", bin)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "paseo")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "paseo-argv")
	t.Setenv("PIGGERY_FAKE_PASEO", log)
	dir := filepath.Join(home, ".piggery")
	o := setupOpts{dir: dir, self: "/opt/piggery", paseoHome: "/h"}
	if _, err := installPaseo(o); err != nil {
		t.Fatal(err)
	}
	changes(t, log)

	statePath := log + ".state"
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state []paseoPlugin
	if err := json.Unmarshal(stateBytes, &state); err != nil || len(state) != 1 {
		t.Fatalf("paseo state: %v %s", err, stateBytes)
	}
	state[0].Enabled = false
	stateBytes, _ = json.Marshal(state)
	if err := os.WriteFile(statePath, stateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	pluginFile := filepath.Join(paseoDir(dir), paseoInstalled)
	before, err := os.ReadFile(pluginFile)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := (&env{dir: dir, stdout: &out}).setup([]string{"--refresh"}); err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	if got := changes(t, log); got != nil {
		t.Fatalf("refresh changed disabled Paseo registration: %q", got)
	}
	after, err := os.ReadFile(pluginFile)
	if err != nil || string(after) != string(before) {
		t.Fatalf("refresh changed disabled Paseo plugin: %q, %v", after, err)
	}
	stateBytes, _ = os.ReadFile(statePath)
	state = nil
	if err := json.Unmarshal(stateBytes, &state); err != nil || len(state) != 1 || state[0].Enabled {
		t.Fatalf("refresh enabled Paseo plugin: %s", stateBytes)
	}
	if !strings.Contains(out.String(), "paseo: skipped (piggery's plugin is disabled)") {
		t.Fatalf("refresh output: %q", out.String())
	}
}

func TestSetupRefreshRepairsSameVersionCodexHookDrift(t *testing.T) {
	old := codexHooksList
	codexHooksList = fakeHooksList
	t.Cleanup(func() { codexHooksList = old })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".piggery")
	if _, err := installCodex(t.TempDir(), codexHome(), "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome(), "hooks.json")
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	b, _ := os.ReadFile(hooksPath)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc.Hooks, "Interrupt")
	b, _ = json.Marshal(doc)
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, installed, drift := codexIntegration(codexHome()); !installed || drift == "" {
		t.Fatalf("same-version hook drift not detected: installed=%v drift=%q", installed, drift)
	}

	var out strings.Builder
	if err := (&env{dir: dir, stdout: &out}).setup([]string{"--refresh"}); err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	if got := codexPiggeryHookCount(mustReadFile(t, hooksPath)); got != len(codexHookEvents) {
		t.Fatalf("refresh restored %d/%d Codex hooks", got, len(codexHookEvents))
	}
	if !strings.Contains(out.String(), "codex: refreshed") {
		t.Fatalf("refresh output: %q", out.String())
	}
}

func TestSetupRefreshDoesNotEnableDisabledCodexMCP(t *testing.T) {
	old := codexHooksList
	codexHooksList = fakeHooksList
	t.Cleanup(func() { codexHooksList = old })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".piggery")
	if _, err := installCodex(t.TempDir(), codexHome(), "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(codexHome(), "config.toml")
	cfg := mustReadFile(t, cfgPath)
	cfg = []byte(strings.Replace(string(cfg), "[mcp_servers.piggery]\n", "[mcp_servers.piggery]\nenabled\t= false # deliberately disabled\n", 1))
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome(), "hooks.json")
	hooksBefore := mustReadFile(t, hooksPath)

	var out strings.Builder
	if err := (&env{dir: dir, stdout: &out}).setup([]string{"--refresh"}); err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	if got := mustReadFile(t, cfgPath); string(got) != string(cfg) {
		t.Fatalf("refresh changed disabled Codex MCP: %s", got)
	}
	if got := mustReadFile(t, hooksPath); string(got) != string(hooksBefore) {
		t.Fatalf("refresh changed hooks for disabled Codex MCP: %s", got)
	}
	if !strings.Contains(out.String(), "codex: skipped (piggery's MCP registration is disabled)") {
		t.Fatalf("refresh output: %q", out.String())
	}
}

func TestSetupRefreshLeavesUnregisteredDshPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".piggery")
	if _, err := installDsh(dir); err != nil {
		t.Fatal(err)
	}
	patch := dshHomePatch()
	if err := os.Remove(patch); err != nil {
		t.Fatal(err)
	}
	entry := local.DshEntry(dir)
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, append(b, []byte("\n// stale unregistered plugin\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := (&env{dir: dir, stdout: &out}).setup([]string{"--refresh"}); err != nil {
		t.Fatalf("setup --refresh: %v\n%s", err, out.String())
	}
	after, err := os.ReadFile(entry)
	if err != nil || string(after) != string(append(b, []byte("\n// stale unregistered plugin\n")...)) {
		t.Fatalf("refresh changed unregistered DSH plugin: %q, %v", after, err)
	}
	if !strings.Contains(out.String(), "dsh: skipped (dsh's piggery row is not registered)") {
		t.Fatalf("refresh output: %q", out.String())
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSetupRefreshOpencodeKeepsHostConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	dir := filepath.Join(home, "piggery")
	if _, err := local.InstallOpencodeExt(local.OpencodeExtDir(dir)); err != nil {
		t.Fatal(err)
	}
	entry := local.OpencodeEntry(dir)
	original, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, append(original, []byte("\n// stale same-version asset\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := opencodeConfig()
	if err := os.MkdirAll(filepath.Dir(cfg), 0700); err != nil {
		t.Fatal(err)
	}
	// No piggery entry: refreshing worker assets must not register the host.
	custom := []byte(`{"plugin":["other-plugin"],"model":"user/model"}`)
	if err := os.WriteFile(cfg, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&env{dir: dir, stdout: &strings.Builder{}}).setup([]string{"--refresh"}); err != nil {
		t.Fatal(err)
	}
	if !local.OpencodeExtCurrent(local.OpencodeExtDir(dir)) {
		t.Fatal("same-version plugin not refreshed")
	}
	if got, err := os.ReadFile(cfg); err != nil || string(got) != string(custom) {
		t.Fatalf("host config changed: %v", err)
	}
}
