package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// replayInterrupts runs an interruptWatch over a capture of Claude Code 2.1.296
// (testdata/fixtures/claude-2.1.296/<name>.jsonl: its hooks and the status its session file had,
// in time order, t in seconds) and returns when (t) it sent its interrupted ends, and the t of the
// last idle the file said. The daemon's turn is
// open from UserPromptSubmit to Stop, as the hooks drive it, and closed by the end it is sent.
func replayInterrupts(t *testing.T, name string, file func(claudeSession) (claudeSession, bool), alive bool) (sent []float64, idleAt float64) {
	t.Helper()
	f, err := os.Open(filepath.Join("../../testdata/fixtures/claude-2.1.296", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type ev struct {
		T       float64 `json:"t"`
		Hook    string  `json:"hook"`
		Status  string  `json:"status"`
		Updated float64 `json:"updated"`
	}
	var evs []ev
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e ev
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
	base := time.Unix(1_800_000_000, 0)
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec * float64(time.Second))) }
	var now float64
	open := false
	w := &interruptWatch{after: claudeIdleAfter,
		read: func() (cs claudeSession, ok bool) {
			for _, e := range evs {
				if e.Status != "" && e.T <= now {
					cs = claudeSession{PeerProtocol: 1, PID: 4434, Status: e.Status, StatusUpdatedAt: at(e.Updated).UnixMilli()}
				}
			}
			return file(cs)
		},
		alive:    func() bool { return alive },
		turnOpen: func() (bool, error) { return open, nil },
		end:      func() error { sent = append(sent, now); open = false; return nil },
	}
	last := evs[len(evs)-1].T + 20
	for now = 0; now <= last; now += 1 {
		for _, e := range evs { // the hooks that came in this second
			if e.Hook != "" && e.T > now-1 && e.T <= now {
				open = e.Hook != "Stop" && e.Hook != "SessionEnd"
			}
		}
		w.poll(at(now))
	}
	for _, e := range evs {
		if e.Status == "idle" {
			idleAt = e.Updated
		}
	}
	return sent, idleAt
}

func asIs(cs claudeSession) (claudeSession, bool) { return cs, cs.Status != "" }

// An Esc leaves a Claude turn with no end: at a permission prompt, while it thinks, or in an Agent SDK
// interrupt. The session file says idle within 0.1 s of it; once it has stood idle for a while the
// open turn is ended, once.
func TestClaudeInterruptEndsTheOpenTurn(t *testing.T) {
	for _, name := range []string{"interrupt-permission", "interrupt-thinking", "interrupt-sdk"} {
		sent, idleAt := replayInterrupts(t, name, asIs, true)
		if len(sent) != 1 || sent[0] < idleAt+claudeIdleAfter.Seconds() {
			t.Errorf("%s: ends sent at %v, want one, %v s after the file went idle at %v (not at the prompt, nor `waiting`)", name, sent, claudeIdleAfter.Seconds(), idleAt)
		}
	}
}

// A turn that is only slow is left alone: Claude thinking (the transcript does not grow for a
// minute), a tool that runs for two minutes (then `shell`, while it goes on in the background),
// and a normal turn whose Stop hook comes just before the file says idle.
func TestClaudeBusyOrStoppedTurnIsLeftAlone(t *testing.T) {
	for _, name := range []string{"thinking", "long-tool", "normal-sdk"} {
		if sent, _ := replayInterrupts(t, name, asIs, true); len(sent) != 0 {
			t.Errorf("%s: ends sent at %v, want none", name, sent)
		}
	}
}

// A watch whose process is gone (a stale file left by a kill -9) or whose file is not there does
// nothing, today's behaviour.
func TestClaudeStaleOrMissingStatusDoesNothing(t *testing.T) {
	missing := func(claudeSession) (claudeSession, bool) { return claudeSession{}, false }
	if sent, _ := replayInterrupts(t, "interrupt-permission", asIs, false); len(sent) != 0 {
		t.Errorf("the process is gone: ends sent at %v, want none", sent)
	}
	if sent, _ := replayInterrupts(t, "interrupt-permission", missing, true); len(sent) != 0 {
		t.Errorf("no file: ends sent at %v, want none", sent)
	}
}

// claudeSessionOf reads the real file Claude wrote (fixture session-file.json) of the process the
// host names, and nothing else: another pid, a file that is not JSON or cut short, another peerProtocol,
// no status yet and a missing file all fail.
func TestClaudeSessionOfReadsTheHostsFile(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/claude-2.1.296/session-file.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(pid string, b []byte) {
		if err := os.WriteFile(filepath.Join(cfg, "sessions", pid+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("4434", raw)
	if cs, ok := claudeSessionOf("claude:4434:1"); !ok || cs.Status != "idle" || cs.StatusUpdatedAt != 1791773590156 {
		t.Fatalf("the host's own file: %+v, %v", cs, ok)
	}
	put("4435", raw) // a file of pid 4434 under another pid's name
	put("4436", []byte(`{"pid":4436,"peerProtocol":1,"sta`))
	put("4438", []byte(`{"pid":4438,"peerProtocol":2,"status":"idle","statusUpdatedAt":1791773590156}`))
	put("4439", []byte(`{"pid":4439,"peerProtocol":1,"statusUpdatedAt":1791773590156}`)) // as right after the SDK starts: no status yet
	for _, host := range []string{"claude:4435:1", "claude:4436:1", "claude:4437:1", "claude:4438:1", "claude:4439:1", "codex:4434:1", "claude:x:1"} {
		if cs, ok := claudeSessionOf(host); ok {
			t.Errorf("%s: read %+v, want nothing", host, cs)
		}
	}
}

// One idle status is ended once however long it stands, and a failed end is tried again.
func TestClaudeInterruptIsEndedOncePerIdle(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	var sent, fail int
	w := &interruptWatch{after: claudeIdleAfter,
		read: func() (claudeSession, bool) {
			return claudeSession{PeerProtocol: 1, Status: "idle", StatusUpdatedAt: base.UnixMilli()}, true
		},
		alive:    func() bool { return true },
		turnOpen: func() (bool, error) { return true, nil }, // still open however many times it is asked
		end: func() error {
			if sent++; fail > 0 {
				fail--
				return os.ErrClosed
			}
			return nil
		},
	}
	fail = 1
	for s := 0; s < 30; s++ {
		w.poll(base.Add(time.Duration(s) * time.Second))
	}
	if sent != 2 {
		t.Fatalf("ends sent = %d, want the failed one and one more", sent)
	}
}

// A person who submits a prompt while the daemon is being asked has a new turn open by the time the
// answer is in: the file says busy again, so the end is not sent (it would close the new turn).
func TestClaudeInterruptSparesATurnStartedMeanwhile(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	status, ends := "idle", 0
	w := &interruptWatch{after: claudeIdleAfter,
		read: func() (claudeSession, bool) {
			return claudeSession{PeerProtocol: 1, Status: status, StatusUpdatedAt: base.UnixMilli()}, true
		},
		alive: func() bool { return true },
		turnOpen: func() (bool, error) {
			status = "busy" // the prompt arrives while the daemon answers: Claude writes busy first
			return true, nil
		},
		end: func() error { ends++; return nil },
	}
	for s := 0; s < 10; s++ {
		w.poll(base.Add(time.Duration(s) * time.Second))
	}
	if ends != 0 {
		t.Fatalf("ends sent = %d, want none: the turn that is open is the new one", ends)
	}
}
