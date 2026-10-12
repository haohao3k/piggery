//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"2.1.283": true, "2.1.139": true, "2.1.138": false, "2.0.999": false, "3.0.0": true, "2.1": false} {
		if got := versionAtLeast(v, claudeExecFormSince); got != want {
			t.Errorf("versionAtLeast(%s) = %v, want %v", v, got, want)
		}
	}
}

// Codex's hook command has no quote in it unless piggery's path needs one: a Codex that escapes the
// quotes of a command (openai/codex #32402) runs none that has any.
func TestCodexHookCommandQuotesOnlyAPathThatNeedsIt(t *testing.T) {
	for self, want := range map[string]string{
		`C:\Users\me\.local\bin\piggery.exe`:     `C:\Users\me\.local\bin\piggery.exe hook codex Stop`,
		`C:\Users\Jo Doe\.local\bin\piggery.exe`: `"C:\Users\Jo Doe\.local\bin\piggery.exe" hook codex Stop`,
		`C:\Tools (x86)\piggery.exe`:             `"C:\Tools (x86)\piggery.exe" hook codex Stop`,
	} {
		if got := codexHookCommand(self, "Stop"); got != want {
			t.Errorf("codexHookCommand(%s) = %s, want %s", self, got, want)
		}
	}
}

// A path with a space goes into Codex's command by its 8.3 name, unquoted: Codex 0.155.1 runs no
// command with a quote in it.
func TestCodexHookCommandShortensAPathWithASpace(t *testing.T) {
	self := filepath.Join(t.TempDir(), "Jo Doe", "piggery.exe")
	if err := os.MkdirAll(filepath.Dir(self), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if shortPath(self) == self {
		t.Skip("this volume has no 8.3 names")
	}
	path, ok := strings.CutSuffix(codexHookCommand(self, "Stop"), " hook codex Stop")
	a, err1 := os.Stat(path)
	b, err2 := os.Stat(self)
	if !ok || strings.ContainsAny(path, `" `) || err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Fatalf("codexHookCommand(%s) = %q; want piggery's 8.3 path, unquoted, then the hook", self, path)
	}
}

// Claude Code 2.1.295 does not run an exec-form hook whose command has a backslash.
func TestClaudeHookHandlerUsesForwardSlashes(t *testing.T) {
	self := `C:\Tools\Jo Doe\piggery.exe`
	h := claudeHookHandler(self, "Stop")
	if h["command"] != "C:/Tools/Jo Doe/piggery.exe" || !claudeHookRunsSelf(h["command"].(string), h["args"].([]string), self) {
		t.Fatalf("handler %v; want forward slashes, and setup to read it as its own", h)
	}
	if claudeHookRunsSelf(self, []string{"hook", "claude", "Stop"}, self) {
		t.Fatal("a handler with backslashes reads as current; setup would keep a hook Claude does not run")
	}
}
