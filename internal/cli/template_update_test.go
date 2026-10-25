package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/server"
	"github.com/sting8k/piggery/manifests"
)

// `template update` through the command: a directory equal to the built-in is current; one the
// user changed is left alone and listed, and with --force moved whole to templates/.backup (not a
// template for list or found) and replaced; a removed one is written again; one that is not in
// the record is the user's own and said so, and only --force overwrites it; a file added counts as
// a difference (a dot file does not); --force carries the deprecated spawn.model|harness|thinking of
// the copy it replaces into config.yaml `roles`, without overwriting an entry that is there; no name does every
// built-in; a name that is not built in is an error.
func TestTemplateUpdate(t *testing.T) {
	spawnLine := regexp.MustCompile(`(?m)^    spawn:[^\n]*\n`)
	dir := t.TempDir()
	if _, err := manifests.Unpack(dir); err != nil {
		t.Fatal(err)
	}
	if _, errs := server.EnsureFiles(dir); len(errs) != 0 { // as setup and the daemon do: the key filler must leave a built-in as it is
		t.Fatal(errs)
	}
	run := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := Main(dir, append([]string{"template"}, args...), &out, &errOut)
		return code, out.String() + errOut.String()
	}
	tpl := filepath.Join(dir, "templates", "lead-peer")
	manifest := filepath.Join(tpl, manifests.ManifestFile)
	orig, _ := os.ReadFile(manifest)
	edit := func() {
		t.Helper()
		if err := os.WriteFile(manifest, append(orig[:len(orig):len(orig)], "# mine\n"...), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if code, out := run("update", "lead-peer"); code != 0 || !strings.Contains(out, "already current") {
		t.Fatalf("current: exit %d\n%s", code, out)
	}

	edit()
	if code, out := run("update", "lead-peer"); code != 0 || !strings.Contains(out, "changed manifest.yaml") || !strings.Contains(out, "--force") {
		t.Fatalf("changed, no --force: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(manifest); !strings.HasSuffix(string(b), "# mine\n") {
		t.Fatal("a template the user changed was touched without --force")
	}
	if _, err := os.Stat(filepath.Join(dir, "templates", ".backup")); err == nil {
		t.Fatal("a backup without --force")
	}

	code, out := run("update", "lead-peer", "--force")
	backups, _ := filepath.Glob(filepath.Join(dir, "templates", ".backup", "lead-peer-*", manifests.ManifestFile))
	if b, _ := os.ReadFile(manifest); code != 0 || !bytes.Equal(b, orig) || len(backups) != 1 || !strings.Contains(out, filepath.Dir(backups[0])) {
		t.Fatalf("--force: exit %d, backups %v\n%s", code, backups, out)
	}
	if b, _ := os.ReadFile(backups[0]); !strings.HasSuffix(string(b), "# mine\n") {
		t.Fatal("the backup is not the user's copy")
	}
	if _, out := run("list"); strings.Contains(out, ".backup") {
		t.Fatalf("the backup shows as a template:\n%s", out)
	}
	if listed, _ := manifests.List(dir); len(listed) != len(manifests.Builtins()) {
		t.Fatalf("listed %v", listed)
	}

	// the old form moves to config on --force: a first entry makes the `roles` key, a second goes into
	// that block, and an entry the config already has for the same key is not overwritten
	set := func(model string, n int) {
		left := n
		old := spawnLine.ReplaceAllStringFunc(string(orig), func(l string) string {
			if left == 0 {
				return l
			}
			left--
			return l + "      model: " + model + "\n"
		})
		if old == string(orig) {
			t.Fatalf("setup: no spawn block in\n%s", orig)
		}
		os.WriteFile(manifest, []byte(old), 0o600)
	}
	set("gpt-x", 1)
	if code, out := run("update", "lead-peer", "--force"); code != 0 || !strings.Contains(out, `moved into config.yaml roles: "lead-peer/lead": {model: gpt-x}`) {
		t.Fatalf("migrate: exit %d\n%s", code, out)
	}
	set("other", 2)
	if code, out := run("update", "lead-peer", "--force"); code != 0 || !strings.Contains(out, "already has") {
		t.Fatalf("migrate again: exit %d\n%s", code, out)
	}
	if got, err := server.LoadSettings(dir); err != nil || len(got.Roles) != 2 || got.Roles["lead-peer/lead"].Model != "gpt-x" || got.Roles["lead-peer/peer"].Model != "other" {
		t.Fatalf("config roles %+v, %v", got.Roles, err)
	}
	if b, _ := os.ReadFile(manifest); !bytes.Equal(b, orig) {
		t.Fatal("the built-in carries the old form again")
	}

	os.WriteFile(filepath.Join(tpl, "notes.md"), []byte("mine"), 0o600)
	os.WriteFile(filepath.Join(tpl, ".DS_Store"), []byte("x"), 0o600)
	if code, out := run("update", "lead-peer"); code != 0 || !strings.Contains(out, "added notes.md") || strings.Contains(out, "DS_Store") {
		t.Fatalf("a file added: exit %d\n%s", code, out)
	}

	os.RemoveAll(tpl)
	if code, out := run("update", "lead-peer"); code != 0 || !strings.Contains(out, "written from the built-in") {
		t.Fatalf("removed: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(manifest); !bytes.Equal(b, orig) {
		t.Fatal("the removed template was not written")
	}

	os.Remove(filepath.Join(dir, "templates", ".builtin.json")) // now it is no one's but the user's
	edit()
	if code, out := run("update", "lead-peer"); code != 0 || !strings.Contains(out, "yours, not piggery's") {
		t.Fatalf("own template, no --force: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(manifest); !strings.HasSuffix(string(b), "# mine\n") {
		t.Fatal("the user's own template was touched without --force")
	}
	if code, out := run("update", "lead-peer", "--force"); code != 0 || !strings.Contains(out, "YOUR OWN") {
		t.Fatalf("own template, --force: exit %d\n%s", code, out)
	}

	if code, out := run("update"); code != 0 || strings.Count(out, ": ") < len(manifests.Builtins()) {
		t.Fatalf("no name: exit %d\n%s", code, out)
	}
	if code, out := run("update", "mine"); code == 0 || !strings.Contains(out, "no built-in template") {
		t.Fatalf("not a built-in: exit %d\n%s", code, out)
	}
}
