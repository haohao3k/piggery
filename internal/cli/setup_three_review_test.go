package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThreeReviewSetupRestoresShortcutIndependently(t *testing.T) {
	home := fakeReviewCodex(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", home)
	e := &env{dir: t.TempDir(), stdout: &strings.Builder{}}
	if err := e.setup([]string{"three-review"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hooks.json", "config.toml"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("addon setup changed %s: %v", name, err)
		}
	}
	if _, err := installCodex(e.dir, home, "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	hooks, cfg := mustReadFile(t, filepath.Join(home, "hooks.json")), mustReadFile(t, filepath.Join(home, "config.toml"))
	if err := os.Remove(codexSkillPath(home)); err != nil {
		t.Fatal(err)
	}
	if err := e.setup([]string{"three-review"}); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectCodexSkill(home); err != nil || state.receipt == nil {
		t.Fatalf("shortcut not restored: %v", err)
	}
	if err := e.setup([]string{"remove", "three-review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshCodex(e.dir, home, "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(codexSkillPath(home)); !os.IsNotExist(err) {
		t.Fatalf("shortcut not removed: %v", err)
	}
	if string(mustReadFile(t, filepath.Join(home, "hooks.json"))) != string(hooks) || string(mustReadFile(t, filepath.Join(home, "config.toml"))) != string(cfg) {
		t.Fatal("addon maintenance changed adapter")
	}
	if err := e.setup([]string{"three-review"}); err != nil {
		t.Fatal(err)
	}
	custom := []byte("Human's custom skill")
	if err := os.WriteFile(codexSkillPath(home), custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.setup([]string{"three-review"}); err == nil {
		t.Fatal("customized skill was accepted for overwrite")
	}
	if string(mustReadFile(t, codexSkillPath(home))) != string(custom) {
		t.Fatal("customized skill overwritten")
	}
}
