package core_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
)

func TestHistoricalQuarantineSurvivesUpstreamAdapterSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piggery.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
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
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e = core.New(db)
	_, err = e.Authenticate(ctx, j.ID, j.Token)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.RuleID != "session.quarantined" {
		t.Fatalf("old token: %v", err)
	}
	if _, err := e.AuthenticateHost(ctx, a.Host); code(err) != core.CodeUnauthorized {
		t.Fatal("quarantine authenticated")
	}
	if ref := e.SessionRef(ctx, j.ID); ref != "" {
		t.Fatalf("quarantined binding still has a wake target: %s", ref)
	}
	if fresh, err := e.JoinAuto(ctx, a); err != nil || fresh.ID == j.ID {
		t.Fatalf("legacy host revived quarantine: %+v %v", fresh, err)
	}
	for _, ref := range []string{"thread-a", "thread-b"} {
		a.HarnessRef = ref
		a.Host = "codex:100:1/" + ref
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
