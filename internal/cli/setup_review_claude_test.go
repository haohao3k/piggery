package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteClaudePluginInstallsDualLaneAdjudicationSkill(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "piggery", "skills", "three-review", "SKILL.md")
	if err := writeSkillFileAtomic(oldPath, []byte(legacyThreeReviewSkill("/opt/piggery"))); err != nil {
		t.Fatal(err)
	}
	if err := writeClaudePlugin(root, "/opt/piggery"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("legacy Claude skill not retired: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "piggery", "skills", "dual-lane-adjudication", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "name: dual-lane-adjudication") {
		t.Fatalf("skill name missing:\n%s", s)
	}
	if !strings.Contains(s, "'/opt/piggery' skills dual-lane-adjudication") {
		t.Fatalf("skill does not point at the installing CLI:\n%s", s)
	}
	if !strings.Contains(s, "$ARGUMENTS") {
		t.Fatalf("Claude invocation arguments placeholder missing:\n%s", s)
	}
}

func TestRefreshClaudeRefreshesDualLaneAdjudicationSkillCache(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
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
	root := filepath.Join(home, ".piggery", "claude")
	self := "/opt/piggery"
	if _, err := installClaude(filepath.Dir(root), self); err != nil {
		t.Fatal(err)
	}
	changes(t, log)

	source := filepath.Join(root, "piggery", "skills", "dual-lane-adjudication", "SKILL.md")
	cache := filepath.Join(home, ".claude", "plugins", "cache", "piggery", "piggery", "skills", "dual-lane-adjudication", "SKILL.md")
	want, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(cache); err != nil || string(got) != string(want) {
		t.Fatalf("initial cached skill: %q, %v", got, err)
	}
	if err := os.WriteFile(cache, []byte("stale skill"), 0o600); err != nil {
		t.Fatal(err)
	}

	msg, err := refreshClaude(root, self)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "refreshed the cached piggery plugin") {
		t.Fatalf("refresh message: %q", msg)
	}
	if got, err := os.ReadFile(cache); err != nil || string(got) != string(want) {
		t.Fatalf("refreshed cached skill: %q, %v", got, err)
	}
	if got := changes(t, log); len(got) != 2 || got[0] != "plugin uninstall piggery@piggery" || got[1] != "plugin install piggery@piggery" {
		t.Fatalf("refresh commands: %q", got)
	}
}

func TestRefreshClaudeSkipsDisabledPluginSkill(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
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
	root := filepath.Join(home, ".piggery", "claude")
	self := "/opt/piggery"
	if _, err := installClaude(filepath.Dir(root), self); err != nil {
		t.Fatal(err)
	}
	changes(t, log)

	statePath := filepath.Join(home, "fake-claude.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Plugins []struct {
			ID          string `json:"id"`
			Enabled     bool   `json:"enabled"`
			InstallPath string `json:"installPath"`
		} `json:"Plugins"`
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Plugins) != 1 {
		t.Fatalf("fake Claude plugins: %s", stateBytes)
	}
	state.Plugins[0].Enabled = false
	stateBytes, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, stateBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cache := filepath.Join(home, ".claude", "plugins", "cache", "piggery", "piggery", "skills", "dual-lane-adjudication", "SKILL.md")
	if err := os.WriteFile(cache, []byte("user cache edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, err := refreshClaude(root, self)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "skipped") {
		t.Fatalf("disabled refresh message: %q", msg)
	}
	if got := changes(t, log); got != nil {
		t.Fatalf("disabled refresh ran commands: %q", got)
	}
	if got, err := os.ReadFile(cache); err != nil || string(got) != "user cache edit" {
		t.Fatalf("disabled refresh changed cache: %q, %v", got, err)
	}
}
