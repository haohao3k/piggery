package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeReviewCodex(t *testing.T) string {
	t.Helper()
	old := codexHooksList
	codexHooksList = fakeHooksList
	t.Cleanup(func() { codexHooksList = old })
	return t.TempDir()
}

func TestCodexReviewSkillLifecycle(t *testing.T) {
	home := fakeReviewCodex(t)
	if _, err := installCodex(t.TempDir(), home, "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	state, err := inspectCodexSkill(home)
	if err != nil || state.receipt == nil || string(state.content) != threeReviewSkill("/bin/piggery", codexReviewSkill) {
		t.Fatalf("installed skill: %+v %v", state, err)
	}
	// A previously installed, still-owned skill refreshes even at the same integration version.
	oldSkill := []byte("old Piggery bootstrap\n")
	if err := os.WriteFile(codexSkillPath(home), oldSkill, 0o600); err != nil {
		t.Fatal(err)
	}
	state.receipt.SHA256 = skillHash(oldSkill)
	state.receipt.TemplateSHA256 = "old-template"
	if _, err := writeJSON(codexSkillReceiptPath(home), state.receipt, true); err != nil {
		t.Fatal(err)
	}
	if _, _, drift := codexIntegration(home); drift == "" {
		t.Fatal("old bootstrap template is not reported as drift")
	}
	if _, err := refreshCodex(t.TempDir(), home, "/opt/new piggery"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(codexSkillPath(home))
	if err != nil || string(got) != threeReviewSkill("/opt/new piggery", codexReviewSkill) {
		t.Fatalf("refreshed skill: %s %v", got, err)
	}
	if st := codexStatus(home, "/opt/new piggery"); len(st.Problems) != 0 {
		t.Fatalf("refreshed status: %+v", st)
	}
	if _, err := removeCodex(home); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("owned install not fully removed: %v %v", entries, err)
	}
}

func TestCodexReviewSkillPreservesCustomizationAndRemoval(t *testing.T) {
	for _, mutate := range []string{"customize", "remove-file", "remove-directory"} {
		t.Run(mutate, func(t *testing.T) {
			home := fakeReviewCodex(t)
			if _, err := installCodex(t.TempDir(), home, "/bin/piggery"); err != nil {
				t.Fatal(err)
			}
			switch mutate {
			case "customize":
				if err := os.WriteFile(codexSkillPath(home), []byte("Human's review skill"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "remove-file":
				if err := os.Remove(codexSkillPath(home)); err != nil {
					t.Fatal(err)
				}
			case "remove-directory":
				if err := os.RemoveAll(filepath.Dir(codexSkillPath(home))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := refreshCodex(t.TempDir(), home, "/opt/piggery"); err == nil || !strings.Contains(err.Error(), "preserved") {
				t.Fatalf("refresh did not report ownership conflict: %v", err)
			}
			if st := codexStatus(home, "/bin/piggery"); len(st.Problems) == 0 {
				t.Fatal("custom or removed skill reported current")
			}
			if _, _, drift := codexIntegration(home); drift == "" {
				t.Fatal("custom or removed skill not reported as drift")
			}
			if msg, err := removeCodex(home); err != nil || !strings.Contains(msg, "preserved") {
				t.Fatalf("remove did not report preserved skill: %s %v", msg, err)
			}
			b, err := os.ReadFile(codexSkillPath(home))
			if mutate == "customize" {
				if err != nil || string(b) != "Human's review skill" {
					t.Fatalf("customized skill was changed: %s %v", b, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("deleted skill was recreated: %v", err)
			}
		})
	}
}

func TestCodexReviewSkillDoesNotClaimUnownedPaths(t *testing.T) {
	for _, mode := range []string{"directory", "skill-link", "root-link", "receipt-link", "special-directory"} {
		t.Run(mode, func(t *testing.T) {
			home := fakeReviewCodex(t)
			outside := t.TempDir()
			var err error
			switch mode {
			case "directory":
				err = os.MkdirAll(filepath.Dir(codexSkillPath(home)), 0o700)
			case "root-link":
				err = os.Symlink(outside, filepath.Join(home, "skills"))
			case "receipt-link":
				err = os.Symlink(filepath.Join(outside, "receipt"), codexSkillReceiptPath(home))
			case "special-directory":
				err = os.MkdirAll(codexSkillPath(home), 0o700)
			case "skill-link":
				if err = os.MkdirAll(filepath.Dir(codexSkillPath(home)), 0o700); err == nil {
					err = os.Symlink(filepath.Join(outside, "skill"), codexSkillPath(home))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := installCodex(t.TempDir(), home, "/bin/piggery"); err == nil {
				t.Fatal("unowned or nonregular skill path accepted")
			}
			if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
				t.Fatalf("host hooks changed before detecting skill conflict: %v", err)
			}
			if files, err := os.ReadDir(outside); err != nil || len(files) != 0 {
				t.Fatalf("installer followed a skill link: %v %v", files, err)
			}
		})
	}
}

func TestCodexReviewSkillPreservesOtherSkillFiles(t *testing.T) {
	home := fakeReviewCodex(t)
	if _, err := installCodex(t.TempDir(), home, "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(filepath.Dir(codexSkillPath(home)), "human-notes.md")
	if err := os.WriteFile(extra, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := removeCodex(home); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(extra); err != nil || string(b) != "keep" {
		t.Fatalf("user addition removed: %s %v", b, err)
	}
}

func TestCodexReviewSkillOutdatedDoesNotRestoreDisabledIntegration(t *testing.T) {
	for _, mode := range []string{"disabled", "no-hooks"} {
		t.Run(mode, func(t *testing.T) {
			home := fakeReviewCodex(t)
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			if _, err := installCodex(t.TempDir(), codexHome(), "/bin/piggery"); err != nil {
				t.Fatal(err)
			}
			cfgPath := filepath.Join(codexHome(), "config.toml")
			cfg := mustReadFile(t, cfgPath)
			if mode == "disabled" {
				cfg = []byte(strings.Replace(string(cfg), "[mcp_servers.piggery]\n", "[mcp_servers.piggery]\nenabled = false\n", 1))
				if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(filepath.Join(codexHome(), "hooks.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(codexSkillPath(codexHome())); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			dir := filepath.Join(home, ".piggery")
			if err := (&env{dir: dir, stdout: &out}).updateOutdated(setupOpts{dir: dir, self: "/opt/piggery"}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "codex: skipped") || strings.Contains(out.String(), "codex: updated") {
				t.Fatalf("misleading update result: %s", out.String())
			}
			if got := mustReadFile(t, cfgPath); string(got) != string(cfg) {
				t.Fatal("disabled/unregistered host config was changed")
			}
			if _, err := os.Lstat(codexSkillPath(codexHome())); !os.IsNotExist(err) {
				t.Fatalf("removed skill restored: %v", err)
			}
			if mode == "no-hooks" {
				if _, err := os.Lstat(filepath.Join(codexHome(), "hooks.json")); !os.IsNotExist(err) {
					t.Fatalf("unregistered hooks restored: %v", err)
				}
			}
		})
	}
}

func TestCodexReviewSkillDetectsInterruptedExecutableUpdate(t *testing.T) {
	home := fakeReviewCodex(t)
	if _, err := installCodex(t.TempDir(), home, "/bin/piggery"); err != nil {
		t.Fatal(err)
	}
	// Simulate host registration moving before the skill write completed.
	cfgPath := filepath.Join(home, "config.toml")
	cfg := mustReadFile(t, cfgPath)
	if err := os.WriteFile(cfgPath, []byte(strings.Replace(string(cfg), "command = \"/bin/piggery\"", "command = \"/opt/piggery\"", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, drift := codexIntegration(home); !strings.Contains(drift, "different executable") {
		t.Fatalf("stale skill command binding not reported: %q", drift)
	}
	if _, err := refreshCodex(t.TempDir(), home, "/opt/piggery"); err != nil {
		t.Fatal(err)
	}
	if _, _, drift := codexIntegration(home); drift != "" {
		t.Fatalf("binding not repaired: %q", drift)
	}
}
