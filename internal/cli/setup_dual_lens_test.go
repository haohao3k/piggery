package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDualLensSetupRestoresShortcutIndependently(t *testing.T) {
	home := fakeReviewCodex(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", home)
	e := &env{dir: t.TempDir(), stdout: &strings.Builder{}}
	if err := e.setup([]string{"dual-lens"}); err != nil {
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
	if err := e.setup([]string{"dual-lens"}); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectCodexSkill(home); err != nil || state.receipt == nil {
		t.Fatalf("shortcut not restored: %v", err)
	}
	if err := e.setup([]string{"remove", "dual-lens"}); err != nil {
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
	if err := e.setup([]string{"dual-lens"}); err != nil {
		t.Fatal(err)
	}
	custom := []byte("Human's custom skill")
	if err := os.WriteFile(codexSkillPath(home), custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.setup([]string{"dual-lens"}); err == nil {
		t.Fatal("customized skill was accepted for overwrite")
	}
	if string(mustReadFile(t, codexSkillPath(home))) != string(custom) {
		t.Fatal("customized skill overwritten")
	}
}

func TestLegacyReviewShortcutMigrationPreservesOwnership(t *testing.T) {
	for _, mode := range []string{"managed", "custom", "removed", "unowned", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			oldPath := reviewSkillPath(home, legacyReviewSkill)
			content := []byte("old managed bootstrap")
			if err := writeSkillFileAtomic(oldPath, content); err != nil {
				t.Fatal(err)
			}
			record := codexSkillReceipt{SHA256: skillHash(content), Executable: "/bin/piggery"}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "unowned" {
				if err := writeSkillFileAtomic(reviewSkillReceiptPath(home, legacyReviewSkill), raw); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "custom":
				if err := os.WriteFile(oldPath, []byte("user changes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "removed", "symlink":
				if err := os.Remove(oldPath); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					target := filepath.Join(t.TempDir(), "custom.md")
					if err := os.WriteFile(target, content, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, oldPath); err != nil {
						t.Fatal(err)
					}
				}
			}
			codexSetupWithReview(home, "/bin/piggery", "adapter ready", false)
			if mode == "managed" {
				if _, err := os.Lstat(oldPath); !os.IsNotExist(err) {
					t.Fatalf("legacy skill not retired: %v", err)
				}
				if state, err := inspectCodexSkill(home); err != nil || !codexReviewSkillCurrent(state, "/bin/piggery") {
					t.Fatalf("new shortcut not current: %v", err)
				}
			} else {
				if _, err := os.Stat(codexSkillPath(home)); !os.IsNotExist(err) {
					t.Fatalf("refresh unexpectedly opted into new shortcut: %v", err)
				}
				if _, err := installCodexReviewShortcut(home, "/bin/piggery", true); err != nil {
					t.Fatal(err)
				}
				if mode == "custom" && string(mustReadFile(t, oldPath)) != "user changes" {
					t.Fatal("custom legacy skill overwritten")
				}
				if mode != "removed" {
					if _, err := os.Lstat(oldPath); err != nil {
						t.Fatalf("legacy user asset removed: %v", err)
					}
				}
			}
		})
	}
}

func TestDualLaneShortcutRefreshKeepsAliasAndPreservesUserChanges(t *testing.T) {
	home := t.TempDir()
	oldPath := reviewSkillPath(home, legacyDualLaneSkill)
	original := []byte("old managed dual-lane bootstrap")
	if err := writeSkillFileAtomic(oldPath, original); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(codexSkillReceipt{SHA256: skillHash(original), Executable: "/old/piggery"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSkillFileAtomic(reviewSkillReceiptPath(home, legacyDualLaneSkill), raw); err != nil {
		t.Fatal(err)
	}
	codexSetupWithReview(home, "/new/piggery", "adapter ready", false)
	if state, err := inspectCodexSkill(home); err != nil || !codexReviewSkillCurrent(state, "/new/piggery") {
		t.Fatalf("canonical shortcut not installed from old managed opt-in: %v", err)
	}
	old, err := inspectReviewSkillForSetup(home, legacyDualLaneSkill, false)
	if err != nil || string(old.content) != dualLensSkill("/new/piggery", legacyDualLaneSkill) {
		t.Fatalf("old shortcut did not forward to dual-lens on new executable: %v", err)
	}
	if _, err := removeCodexSkill(home); err != nil {
		t.Fatal(err)
	}
	codexSetupWithReview(home, "/new/piggery", "adapter ready", false)
	for _, p := range []string{oldPath, codexSkillPath(home)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("removed shortcut resurrected: %s %v", p, err)
		}
	}
	if err := writeSkillFileAtomic(oldPath, original); err != nil {
		t.Fatal(err)
	}
	if err := writeSkillFileAtomic(reviewSkillReceiptPath(home, legacyDualLaneSkill), raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("my custom instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	codexSetupWithReview(home, "/new/piggery", "adapter ready", false)
	if _, err := os.Stat(codexSkillPath(home)); !os.IsNotExist(err) {
		t.Fatal("custom legacy skill opted into a new shortcut")
	}
	if _, err := installCodexReviewShortcut(home, "/new/piggery", true); err != nil {
		t.Fatal(err)
	}
	if string(mustReadFile(t, oldPath)) != "my custom instructions" {
		t.Fatal("custom legacy skill overwritten")
	}
}
