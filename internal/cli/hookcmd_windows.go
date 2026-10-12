//go:build windows

package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/sting8k/piggery/internal/driver/local"
)

// The hook commands `piggery setup` writes into Claude Code's plugin and Codex's hooks.json:
//   - Codex runs the command string as `cmd.exe /C "<command>"` (openai/codex #38168): a quoted
//     executable path first, then plain words, is the shape that works there. A Codex from before
//     that passed the string as one argument, its quotes escaped as \", which cmd.exe does not read
//     (#32402): no command with a quote in it ran (measured on Codex 0.155.1). So a path cmd.exe
//     would not take as one word goes in by its 8.3 name, which has no space, and is quoted only
//     where the volume has no 8.3 names.
//   - Claude Code runs a command string in Git Bash, or in PowerShell when Git Bash is not
//     installed, so no one quoting is right. A handler with `args` is spawned directly (its hooks
//     reference, "Exec form"), but Claude Code 2.1.295 does not run one whose command has a
//     backslash, and runs it with forward slashes, a space included (measured): self goes in with
//     forward slashes, which Windows reads as the same path.

// codexHookCommand is the command string Codex runs for piggery's hook of event.
func codexHookCommand(self, event string) string {
	if strings.ContainsAny(self, cmdSpecial) {
		if short := shortPath(self); !strings.ContainsAny(short, cmdSpecial) {
			self = short
		} else {
			self = `"` + self + `"`
		}
	}
	return self + " hook codex " + event
}

// cmdSpecial are the characters a file name must be quoted for in cmd.exe (its own help, cmd /?,
// which lists ~ too; cmd.exe reads a ~ in a path as it is, and every 8.3 name has one).
const cmdSpecial = " \t&()[]{}^=;!'+,`"

// shortPath is p's 8.3 name, or p when it has none (a volume with 8.3 names off, a missing file).
func shortPath(p string) string {
	long, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

// claudeHookHandler is the handler piggery's hook of event is in Claude's hooks.json.
func claudeHookHandler(self, event string) map[string]any {
	return map[string]any{"type": "command", "command": filepath.ToSlash(self), "args": []string{"hook", "claude", event}, "timeout": 10}
}

// claudeHookRunsSelf: a hooks.json handler (its command and args) is piggery's hook of this binary,
// as setup writes it now (one with backslashes is stale: Claude Code does not run it).
func claudeHookRunsSelf(command string, args []string, self string) bool {
	return command == filepath.ToSlash(self) && len(args) == 3 && slices.Equal(args[:2], []string{"hook", "claude"})
}

// claudeExecFormSince is the first Claude Code that reads `args` in a hook (its changelog, 2.1.139).
// An older one ignores it and runs `command` alone, here the bare piggery.exe, through bash.
const claudeExecFormSince = "2.1.139"

// claudeHooksSupported: the installed Claude Code reads the exec form setup writes. A version that
// cannot be read is let through.
func claudeHooksSupported() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v, err := local.HarnessVersion(ctx, claudeBin)
	if err != nil || versionAtLeast(v, claudeExecFormSince) {
		return nil
	}
	return fmt.Errorf("claude: Claude Code %s ignores the `args` of a hook, so piggery's hooks would run without their arguments; update it to %s or later", v, claudeExecFormSince)
}

// versionAtLeast: dotted version v is min or later.
func versionAtLeast(v, min string) bool {
	num := func(s string) []int {
		var n []int
		for _, p := range strings.Split(s, ".") {
			i, _ := strconv.Atoi(p)
			n = append(n, i)
		}
		return n
	}
	a, b := num(v), num(min)
	for i := range b {
		if i >= len(a) || a[i] != b[i] {
			return i < len(a) && a[i] > b[i]
		}
	}
	return true
}
