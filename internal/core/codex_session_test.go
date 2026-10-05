package core_test

import (
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
)

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

func TestRetiredAmbiguousBindingUsesUpstreamLifecycle(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	a := core.JoinAutoArgs{Harness: "codex", Mode: "interactive", Host: "codex:100:1", HarnessRef: "thread-a", Cwd: t.TempDir()}
	old, err := e.JoinAuto(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO participant_refs(ref,participant_id) VALUES ('thread-b',?)`, old.ID); err != nil {
		t.Fatal(err)
	}
	// Same ordinary upstream fields used by the one-time fork database conversion.
	if _, err := db.Exec(`UPDATE participants SET state='gone', left_at=1, host=NULL, token_hash='' WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Authenticate(ctx, old.ID, old.Token); code(err) != core.CodeUnauthorized {
		t.Fatalf("old token accepted: %v", err)
	}
	if _, err := e.AuthenticateHost(ctx, a.Host); code(err) != core.CodeUnauthorized {
		t.Fatalf("old host accepted: %v", err)
	}
	for _, ref := range []string{"thread-a", "thread-b"} {
		a.HarnessRef = ref
		a.Host = "codex:100:1/" + ref
		fresh, err := e.JoinAuto(ctx, a)
		if err != nil || fresh.ID == old.ID {
			t.Fatalf("retired history reused: %+v %v", fresh, err)
		}
	}
	var aliasOwner string
	if err := db.QueryRow(`SELECT participant_id FROM participant_refs WHERE ref='thread-b'`).Scan(&aliasOwner); err != nil || aliasOwner != old.ID {
		t.Fatalf("historical alias changed: %s %v", aliasOwner, err)
	}
}
