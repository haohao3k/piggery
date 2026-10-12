package core_test

import (
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
)

// A session forked outside any process (Paseo's Rewind) joins with the id it was forked from
// (ParentRef): it goes on as the participant of a parent that is gone, with the new id recorded so
// a later join by it finds the same row; a parent that is still live is not taken over, the fork is
// a separate solo (#13).
func TestForkOfAGoneParentGoesOnAsIt(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := core.New(db)
	dir := t.TempDir()
	join := func(ref, parent, host string) string {
		t.Helper()
		j, err := e.JoinAuto(ctx, core.JoinAutoArgs{Cwd: dir, Harness: "claude", Mode: "interactive", HarnessRef: ref, ParentRef: parent, Host: host})
		if err != nil {
			t.Fatal(err)
		}
		return j.ID
	}
	parent := join("S1", "", "claude:1:1")
	live := join("L1", "", "claude:3:1")
	if _, err := db.Exec(`UPDATE participants SET state='gone' WHERE id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if got := join("S2", "S1", "claude:2:1"); got != parent {
		t.Fatalf("fork of a gone parent = %s, want the parent %s", got, parent)
	}
	var state, ref string
	if err := db.QueryRow(`SELECT state, session_ref FROM participants WHERE id=?`, parent).Scan(&state, &ref); err != nil || state == "gone" || ref != "S2" {
		t.Fatalf("parent after the fork: state %q session_ref %q, %v", state, ref, err)
	}
	if got := join("S2", "", "claude:2:1"); got != parent {
		t.Fatalf("a later join by the fork's id = %s, want the parent %s", got, parent)
	}
	if got := join("L2", "L1", "claude:4:1"); got == live || got == parent {
		t.Fatalf("fork next to a live parent = %s, want a separate solo", got)
	}
}
