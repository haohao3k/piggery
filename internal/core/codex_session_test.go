package core_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
	"github.com/sting8k/piggery/manifests"
)

// Desktop app-server hosts several chats, even in the same directory. Exercise the
// concrete incident: A's later hook must not change B's identity, project or wake ref.
func TestCodexThreadsUnderOneHostStayIsolated(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	home := t.TempDir()
	if err := manifests.Unpack(home); err != nil {
		t.Fatal(err)
	}
	e := core.New(db, core.WithTemplates(func(name, _ string) (string, error) { return manifests.Resolve(name, home) }))
	const host = "codex:100:1"
	rootA, rootB := t.TempDir(), t.TempDir()
	rootA, _ = filepath.EvalSymlinks(rootA)
	rootB, _ = filepath.EvalSymlinks(rootB)
	join := func(ref, cwd string) core.JoinResult {
		t.Helper()
		j, err := e.JoinAuto(ctx, core.JoinAutoArgs{Harness: "codex", Mode: "interactive", Host: host, HarnessRef: ref, Cwd: cwd})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	a, b, sameProject := join("thread-a", rootA), join("thread-b", rootB), join("thread-c", rootA)
	if a.ID == b.ID || a.ID == sameProject.ID || b.ID == sameProject.ID {
		t.Fatal("threads collapsed")
	}
	if again := join("thread-a", rootA); again.ID != a.ID || again.RunID != a.RunID {
		t.Fatalf("duplicate hook changed identity: %+v", again)
	}
	for _, want := range []struct{ id, ref, cwd string }{{a.ID, "thread-a", rootA}, {b.ID, "thread-b", rootB}, {sameProject.ID, "thread-c", rootA}} {
		c, err := e.AuthenticateSession(ctx, host, want.ref, want.cwd)
		if err != nil || c.ParticipantID != want.id {
			t.Fatalf("auth %s: %+v %v", want.ref, c, err)
		}
		if got := e.SessionRef(ctx, want.id); got != want.ref {
			t.Fatalf("wake ref = %s, want %s", got, want.ref)
		}
	}
	for _, call := range []func() error{
		func() error { _, err := e.AuthenticateHost(ctx, host); return err },
		func() error { _, err := e.AuthenticateSession(ctx, host, "", rootA); return err },
		func() error { _, err := e.AuthenticateSession(ctx, host, "thread-a", rootB); return err },
		func() error {
			_, err := e.JoinAuto(ctx, core.JoinAutoArgs{Harness: "codex", Host: host, HarnessRef: "thread-a", Cwd: rootB})
			return err
		},
	} {
		if code(call()) != core.CodeUnauthorized {
			t.Fatal("unsafe binding accepted")
		}
	}

	// Found uses the actual request's participant, never the newest sibling under the host.
	ca, _ := e.AuthenticateSession(ctx, host, "thread-a", rootA)
	ta, err := e.Agent(ctx, ca, core.AgentArgs{Action: core.AgentFound, Template: "p2p"})
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := e.AuthenticateSession(ctx, host, "thread-b", rootB)
	tb, err := e.Agent(ctx, cb, core.AgentArgs{Action: core.AgentFound, Template: "p2p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ team, root string }{{ta.TeamID, rootA}, {tb.TeamID, rootB}} {
		var root string
		if err := db.QueryRow(`SELECT root_cwd FROM teams WHERE id=?`, want.team).Scan(&root); err != nil {
			t.Fatal(err)
		}
		if root != want.root {
			t.Fatalf("found rooted at sibling project: %s", root)
		}
	}
	ca, _ = e.AuthenticateSession(ctx, host, "thread-a", rootA)
	cb, _ = e.AuthenticateSession(ctx, host, "thread-b", rootB)
	if _, err := e.Send(ctx, ca, core.SendArgs{To: tb.TeamName, Body: "explicit cross-team report"}); err != nil {
		t.Fatal(err)
	}
	mail, err := e.Inbox(ctx, cb, core.InboxArgs{})
	if err != nil || len(mail) != 1 {
		t.Fatalf("recipient mail = %+v %v", mail, err)
	}
	other, _ := e.AuthenticateSession(ctx, host, "thread-c", rootA)
	if mail, err := e.Inbox(ctx, other, core.InboxArgs{}); err != nil || len(mail) != 0 {
		t.Fatalf("sibling received foreign mail: %+v %v", mail, err)
	}
}

func TestCodexResumeRequiresSameThreadAndRoot(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	root := t.TempDir()
	a := core.JoinAutoArgs{Harness: "codex", Mode: "interactive", Host: "codex:100:1", HarnessRef: "thread-a", Cwd: root}
	j, err := e.JoinAuto(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	a.Host = "codex:200:1"
	resumed, err := e.JoinAuto(ctx, a)
	if err != nil || resumed.ID != j.ID || resumed.RunID == j.RunID {
		t.Fatalf("resume = %+v %v", resumed, err)
	}
	if _, err := e.AuthenticateSession(ctx, "codex:100:1", a.HarnessRef, root); code(err) != core.CodeUnauthorized {
		t.Fatal("old process still owns thread")
	}
	// clear is insufficient evidence of lineage in a multiplexed host. A new thread stays separate.
	a.Source, a.HarnessRef = "clear", "thread-new"
	cleared, err := e.JoinAuto(ctx, a)
	if err != nil || cleared.ID == j.ID {
		t.Fatalf("clear aliased another thread: %+v %v", cleared, err)
	}
	a.HarnessRef, a.Cwd = "thread-a", t.TempDir()
	if _, err := e.JoinAuto(ctx, a); code(err) != core.CodeUnauthorized {
		t.Fatalf("resume changed project: %v", err)
	}
}

func TestQuarantinedCodexHistoryCannotBeRebound(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	a := core.JoinAutoArgs{Harness: "codex", Mode: "interactive", Host: "codex:100:1", HarnessRef: "thread-a", Cwd: t.TempDir()}
	j, err := e.JoinAuto(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE participants SET binding_quarantined=1 WHERE id=?`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO participant_refs(ref,participant_id) VALUES ('thread-b',?)`, j.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.Authenticate(ctx, j.ID, j.Token)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.RuleID != "session.quarantined" {
		t.Fatalf("old token: %v", err)
	}
	if _, err := e.AuthenticateSession(ctx, a.Host, a.HarnessRef, a.Cwd); code(err) != core.CodeUnauthorized {
		t.Fatal("quarantine authenticated")
	}
	if ref := e.SessionRef(ctx, j.ID); ref != "" {
		t.Fatalf("quarantined binding still has a wake target: %s", ref)
	}
	for _, ref := range []string{"thread-a", "thread-b"} {
		a.HarnessRef = ref
		fresh, err := e.JoinAuto(ctx, a)
		if err != nil || fresh.ID == j.ID {
			t.Fatalf("contaminated history reused: %+v %v", fresh, err)
		}
	}
	var aliases int
	if err := db.QueryRow(`SELECT COUNT(*) FROM participant_refs WHERE participant_id=?`, j.ID).Scan(&aliases); err != nil || aliases != 1 {
		t.Fatalf("historical alias erased: %d %v", aliases, err)
	}
}

func TestReviewReturnAddressDoesNotFollowReusedDisplayName(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	join := func(ref, name string) (core.JoinResult, core.Caller) {
		t.Helper()
		j, err := e.JoinAuto(ctx, core.JoinAutoArgs{Cwd: t.TempDir(), Harness: "pi", Mode: "interactive", HarnessRef: ref, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		c, err := e.Authenticate(ctx, j.ID, j.Token)
		if err != nil {
			t.Fatal(err)
		}
		return j, c
	}
	old, caller := join("request-a", "requester")
	_, sender := join("review-run", "reviewer")
	who, err := e.Who(ctx, caller)
	if err != nil || !strings.Contains(core.RenderWho(who, old.ID), "id="+old.ID) {
		t.Fatal("launcher cannot capture its stable return ID")
	}
	if _, err := e.HarnessEvent(ctx, caller, core.HarnessEventArgs{Event: core.HarnessSessionEnd}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := join("request-b", "requester")
	if _, err := e.Send(ctx, sender, core.SendArgs{To: old.ID, Body: "review result"}); err == nil {
		t.Fatal("stale return endpoint accepted")
	}
	var mail int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE to_id=?`, fresh.ID).Scan(&mail); err != nil || mail != 0 {
		t.Fatalf("report leaked to reused display name: %d %v", mail, err)
	}
}

func TestClaudeHostRootMismatchDoesNotMutateBinding(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	a := core.JoinAutoArgs{Harness: "claude", Host: "claude:100:1", HarnessRef: "session-a", Cwd: t.TempDir()}
	j, err := e.JoinAuto(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	a.HarnessRef, a.Cwd = "session-b", t.TempDir()
	if _, err := e.JoinAuto(ctx, a); code(err) != core.CodeUnauthorized {
		t.Fatalf("wrong-root join: %v", err)
	}
	if ref := e.SessionRef(ctx, j.ID); ref != "session-a" {
		t.Fatalf("failed join changed binding: %s", ref)
	}
	var aliases int
	if err := db.QueryRow(`SELECT COUNT(*) FROM participant_refs WHERE participant_id=?`, j.ID).Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("failed join inserted aliases: %d %v", aliases, err)
	}
}
