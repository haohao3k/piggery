package core_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
)

// A member's assignment is the latest mail marked op assign (a spawn task is one), tracked through
// its reply chain: a handback, a rework, a note that is not a task, and a later assign that replaces it.
func TestAssignment(t *testing.T) {
	f := newAgentFixture(t)
	w := f.spawn(t, f.lead, "w1")
	send := func(from core.Caller, a core.SendArgs) core.SendResult {
		t.Helper()
		r, err := f.e.Send(ctx, from, a)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	assignment := func() core.Assignment {
		t.Helper()
		s, err := f.e.State(ctx, core.StateArgs{})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range s.Teams[0].Members {
			if m.Name == "w1" && m.Assignment != nil {
				return *m.Assignment
			}
		}
		t.Fatal("w1 has no assignment")
		return core.Assignment{}
	}

	a := assignment()
	if a.Title != "do w1" || a.From != "lead" || a.Latest != nil {
		t.Fatalf("spawn task = %+v; want it, from lead, no reply", a)
	}
	handback := send(w, core.SendArgs{To: "lead", Body: "done", ReplyTo: fmt.Sprintf("#%d", a.Seq)})
	if l := assignment().Latest; l == nil || l.Seq != handback.Seq || !l.ByMember {
		t.Fatalf("after handback latest = %+v; want #%d by the member", l, handback.Seq)
	}
	// The lead sees the worker's handback labelled with the worker's real role and the relation.
	if in, err := f.e.Inbox(ctx, f.lead, core.InboxArgs{}); err != nil || len(in) == 0 || !strings.HasSuffix(in[len(in)-1].FromLabel, ", reports to you)") {
		t.Fatalf("lead inbox after the handback = %+v, %v; want the sender labelled (<role>, reports to you)", in, err)
	}
	note := send(f.lead, core.SendArgs{To: "w1", Body: "I restart the daemon"})
	if a2 := assignment(); a2.Seq != a.Seq || a2.Newer == nil || a2.Newer.Seq != note.Seq {
		t.Fatalf("after a note = %+v; want the same assignment and the note as newer", a2)
	}
	rework := send(f.lead, core.SendArgs{To: "w1", Body: "not enough", ReplyTo: fmt.Sprintf("#%d", handback.Seq)})
	if a2 := assignment(); a2.Latest == nil || a2.Latest.Seq != rework.Seq || a2.Latest.ByMember || a2.Newer != nil {
		t.Fatalf("after rework = %+v; want the rework newest, by the assigner, no newer", a2)
	}
	next := send(f.lead, core.SendArgs{To: "w1", Op: core.OpAssign, Body: "## **Second** task\nmore"})
	if a2 := assignment(); a2.Seq != next.Seq || a2.Title != "Second task" || a2.Latest != nil {
		t.Fatalf("after a later assign = %+v; want #%d titled %q", a2, next.Seq, "Second task")
	}

	if _, err := f.e.Send(ctx, f.lead2, core.SendArgs{To: "w1", Op: core.OpAssign, Body: "mine"}); rule(err) != "permission/assign.not_reports_to" {
		t.Fatalf("assign by a non-reports_to: %v", err)
	}
}
