package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/proto"
	"github.com/sting8k/piggery/internal/store"
)

// What Claude sends when one process changes its session id other than by /clear, through the real
// hook mapping: /branch (SessionEnd resume for the old id, SessionStart fork for the new one) keeps
// the participant, the process goes on in it (#25); an in-process /resume of a session some other
// participant has moves the host to that participant and ends the one it left.
func TestClaudeInProcessSessionSwitch(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	dir := t.TempDir()
	hook := func(host, name, input string) {
		t.Helper()
		var in claudeHookInput
		if err := json.Unmarshal([]byte(input), &in); err != nil {
			t.Fatal(err)
		}
		if name == "SessionStart" {
			if _, err := e.JoinAuto(ctx, core.JoinAutoArgs{Cwd: dir, Harness: "claude", Mode: "interactive",
				HarnessRef: in.SessionID, Source: in.Source, Host: host}); err != nil {
				t.Fatal(err)
			}
		}
		a, ok := claudeHookEvent(name, in)
		if !ok {
			return
		}
		c, err := e.AuthenticateHost(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.HarnessEvent(ctx, c, a); err != nil {
			t.Fatal(err)
		}
	}
	idOf := func(host string) string {
		t.Helper()
		c, err := e.AuthenticateHost(ctx, host)
		if err != nil {
			t.Fatalf("host %s: %v", host, err)
		}
		return c.ParticipantID
	}
	state := func(id string) (s string) {
		db.QueryRow(`SELECT state FROM participants WHERE id=?`, id).Scan(&s)
		return s
	}

	const h1, h2 = "claude:5151:1", "claude:5252:1"
	hook(h1, "SessionStart", `{"session_id":"S1","source":"startup"}`)
	a := idOf(h1)
	hook(h1, "SessionEnd", `{"session_id":"S1","reason":"resume"}`)
	hook(h1, "SessionStart", `{"session_id":"S2","source":"fork"}`)
	if idOf(h1) != a || state(a) == "gone" {
		t.Fatalf("after /branch the host is another participant or the original is %s", state(a))
	}

	// Another process ran session Y and quit: its participant is gone. A's process /resumes Y.
	hook(h2, "SessionStart", `{"session_id":"Y","source":"startup"}`)
	c := idOf(h2)
	hook(h2, "SessionEnd", `{"reason":"prompt_input_exit"}`)
	if state(c) != "gone" {
		t.Fatalf("setup: Y's participant is %s", state(c))
	}
	hook(h1, "SessionEnd", `{"session_id":"S2","reason":"resume"}`)
	hook(h1, "SessionStart", `{"session_id":"Y","source":"resume"}`)
	if idOf(h1) != c || state(a) != "gone" {
		t.Fatalf("after /resume of Y the host is not Y's participant, or the one it left is %s", state(a))
	}
}

// A SessionEnd from a host the daemon does not know (an old process's, after a restart) is not a
// reason to bring its participant back only to end it again: nothing is joined (#13).
func TestHookSessionEndDoesNotRejoin(t *testing.T) {
	dir, err := os.MkdirTemp("", "pghooke")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbHarnessEvent {
			return &core.Error{Code: core.CodeUnauthorized, RuleID: "host.unknown", Message: "no live session with this host"}
		}
		return map[string]any{}
	})
	t.Setenv("PIGGERY_ID", "")
	t.Setenv("PIGGERY_TOKEN", "")
	t.Setenv("PIGGERY_DISABLED", "")
	old := sessionHost
	sessionHost = func(...string) string { return "claude:9:1" }
	t.Cleanup(func() { sessionHost = old })
	e := &env{dir: dir, stdout: io.Discard}
	e.runHook("claude", "SessionEnd", strings.NewReader(`{"session_id":"s1","reason":"other","cwd":"/w"}`), io.Discard)
	for _, c := range d.calls {
		if c.Verb == proto.VerbJoinAuto {
			t.Fatalf("SessionEnd rejoined the session: %d calls", len(d.calls))
		}
	}
}

// The session a Claude transcript was forked from is on its first entry (forkedFrom.sessionId, as
// the SDK's forkSession and /branch write it); the MCP server, which has no transcript path,
// finds the file by the session id under the Claude config dir.
func TestForkParent(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	proj := filepath.Join(cfg, "projects", "-w-api")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	fork := `{"type":"user","sessionId":"S2","forkedFrom":{"sessionId":"S1","messageUuid":"m1"}}` + "\n" + `{"type":"assistant"}` + "\n"
	plain := `{"type":"user","sessionId":"S3"}` + "\n"
	for name, text := range map[string]string{"S2.jsonl": fork, "S3.jsonl": plain} {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := claudeForkParent("S2", ""); got != "S1" {
		t.Fatalf("by session id: %q, want S1", got)
	}
	if got := claudeForkParent("S2", filepath.Join(proj, "S2.jsonl")); got != "S1" {
		t.Fatalf("by path: %q, want S1", got)
	}
	if got := claudeForkParent("S3", ""); got != "" {
		t.Fatalf("a session that is no fork: %q", got)
	}
	if got := claudeForkParent("nosuch", ""); got != "" {
		t.Fatalf("no transcript: %q", got)
	}
}
