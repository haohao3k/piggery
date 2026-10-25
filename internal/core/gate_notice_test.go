package core_test

import (
	"slices"
	"testing"
	"time"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
)

// The engine tells the operator, at the gate's turn_end and only from state: one team
// walked through the rules in order. alice is the gate (the earliest session), bob a member.
func TestGateNotice(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var sunk []string
	f := newFixture(t, nil, core.WithClock(func() time.Time { return now }),
		core.WithNotifySink(func(id string) { sunk = append(sunk, id) }))
	ev := func(c core.Caller, event, prompt, outcome string) core.HarnessEventResult {
		t.Helper()
		now = now.Add(time.Second)
		r, err := f.e.HarnessEvent(ctx, c, core.HarnessEventArgs{Event: event, PromptID: prompt, Outcome: outcome})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	start := func(c core.Caller, prompt string) { ev(c, core.HarnessTurnStart, prompt, "") }
	end := func(c core.Caller, prompt, outcome string) core.HarnessEventResult {
		return ev(c, core.HarnessTurnEnd, prompt, outcome)
	}
	send := func(from core.Caller, to, body string) {
		t.Helper()
		now = now.Add(time.Second)
		f.send(t, from, core.SendArgs{To: to, Body: body})
	}
	// notices are what the hook was told since the last call.
	notices := func() []core.NotifyMail {
		t.Helper()
		var out []core.NotifyMail
		for _, id := range sunk {
			m, err := f.e.NotifyMail(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		sunk = nil
		return out
	}
	kinds := func() []string {
		var out []string
		for _, m := range notices() {
			out = append(out, m.Kind)
		}
		return out
	}
	want := func(step string, got []string, kinds ...string) {
		t.Helper()
		if !slices.Equal(got, kinds) {
			t.Fatalf("%s: notices %v, want %v", step, got, kinds)
		}
	}
	const ok = core.HarnessOutcomeOK
	alice, bob := f.alice, f.bob

	// rule 2: a turn given no mail (the Human chatted with the gate) tells nothing
	start(alice, "c1")
	end(alice, "c1", ok)
	want("chat turn", kinds())

	// rule 3: a turn on mail where the gate sent nothing is a reply
	send(bob, "alice", "report")
	start(alice, "a1")
	end(alice, "a1", ok)
	got := notices()
	if len(got) != 1 || got[0].Kind != "reply" || got[0].Gate != "alice" || got[0].Team != "peers" || got[0].Dir == "" || got[0].FromLabel != "engine" {
		t.Fatalf("reply = %+v", got)
	}

	// rule 5: the gate dispatches while a member works: it is coordinating
	start(bob, "b1")
	send(bob, "alice", "q?")
	start(alice, "a2")
	send(alice, "bob", "do x")
	end(alice, "a2", ok)
	want("dispatch while bob works", kinds())

	// a member never notifies, whatever its turn had
	end(bob, "b1", ok) // blocked: "do x" is given first
	end(bob, "b1", ok)
	want("member turn", kinds())

	// rule 4: the gate's last send and nobody left to work: settled
	send(bob, "alice", "done")
	start(alice, "a3")
	send(alice, "bob", "thanks")
	start(bob, "b2")
	end(bob, "b2", ok)
	end(alice, "a3", ok)
	want("final send, team settled", kinds(), "settled")

	// rule 1: mail still pending when the turn ends (the blocks ran out) leaves it to the next turn
	send(bob, "alice", "m0")
	start(alice, "a4")
	for i := range 8 {
		send(bob, "alice", "m"+string(rune('1'+i)))
		if r := end(alice, "a4", ok); !r.Block {
			t.Fatalf("end %d with new mail did not block", i)
		}
	}
	send(bob, "alice", "last")
	if r := end(alice, "a4", ok); r.Block {
		t.Fatal("blocked past the limit")
	}
	want("mail pending", kinds())
	start(alice, "a5") // the next turn gets it and decides
	end(alice, "a5", ok)
	want("the turn after", kinds(), "reply")

	// a failed turn given mail is stuck mail: failed; one given none, and an interrupted one, tell nothing
	send(bob, "alice", "x")
	start(alice, "a6")
	end(alice, "a6", core.HarnessOutcomeFailed)
	want("failed with mail", kinds(), "failed")
	start(alice, "a7")
	end(alice, "a7", ok)
	want("the retry", kinds(), "reply")
	start(alice, "a8")
	end(alice, "a8", core.HarnessOutcomeFailed)
	want("failed chat turn", kinds())
	send(bob, "alice", "y")
	start(alice, "a9")
	end(alice, "a9", core.HarnessOutcomeIntr)
	want("interrupted", kinds())

	// rule 4: mail waiting for a member that is gone but still in the team (its wake is refused or not
	// yet started) is mail that waits: not settled
	send(bob, "alice", "bye")
	if err := f.e.Presence(ctx, bob, core.PresenceArgs{Event: core.PresenceShutdown}); err != nil {
		t.Fatal(err)
	}
	start(alice, "a10")
	send(alice, "bob", "one more thing")
	end(alice, "a10", ok)
	want("mail waits for a gone member", kinds())

	// a solo is its own gate: its notice has no team, and its directory is its cwd
	cwd := t.TempDir()
	j, err := f.e.JoinAuto(ctx, core.JoinAutoArgs{Cwd: cwd, Harness: "pi", Mode: "rpc", HarnessRef: "solo-1"})
	if err != nil {
		t.Fatal(err)
	}
	solo, _ := f.e.Authenticate(ctx, j.ID, j.Token)
	var name string
	if err := f.db.QueryRow(`SELECT name FROM participants WHERE id=?`, j.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	send(alice, name, "for you")
	start(solo, "s1")
	end(solo, "s1", ok)
	if got := notices(); len(got) != 1 || got[0].Kind != "reply" || got[0].Team != "" || got[0].Dir == "" || got[0].Gate == "" {
		t.Fatalf("solo reply = %+v", got)
	}
}

// A headless worker that has become the gate (no session is left in its team) tells nothing: it
// answers for no Human.
func TestHeadlessGateNeverNotifies(t *testing.T) {
	f := newLiveFixture(t)
	var sunk []string
	core.WithNotifySink(func(id string) { sunk = append(sunk, id) })(f.e)
	f.spawn(t)
	s := f.rt.Starts()[0]
	w, err := f.e.Authenticate(ctx, s.ParticipantID, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.Identify(ctx, w, core.IdentifyArgs{RunID: w.RunID, Harness: "pi", Mode: "headless"}); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Presence(ctx, f.lead, core.PresenceArgs{Event: core.PresenceShutdown}); err != nil { // the session leaves: w is the gate
		t.Fatal(err)
	}
	// its task is mail: a turn on it that sends nothing would be a reply for a session gate
	for _, a := range []core.HarnessEventArgs{
		{Event: core.HarnessTurnStart, PromptID: "w1"},
		{Event: core.HarnessTurnEnd, PromptID: "w1", Outcome: core.HarnessOutcomeOK},
	} {
		if _, err := f.e.HarnessEvent(ctx, w, a); err != nil {
			t.Fatal(err)
		}
	}
	if len(sunk) != 0 {
		t.Fatalf("a headless gate notified: %v", sunk)
	}
}

// A taskforce the gate called is the team's work still to come: the gate's last send while the
// taskforce has it is not settled; once the gate closed the taskforce, it is.
func TestGateNoticeWaitsForItsTaskforce(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const tf = "template: tf\ntaskforce: {}\nauto_join_role: chair\nroles:\n  chair: {tools: [send, inbox]}\n"
	var sunk []string
	e := core.New(db, core.WithRuntime(&fakeRuntime{}), core.WithNotifySink(func(id string) { sunk = append(sunk, id) }),
		core.WithTemplates(func(string, string) (string, error) { return tf, nil }))
	team, err := e.TeamUp(ctx, core.TeamUpArgs{Manifest: "template: t\nauto_join_role: lead\nroles:\n  lead: {tools: [send, inbox, agent]}\nrouting: [{from: lead, to: lead, allow: true}]\n", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	join := func(name string) core.Caller {
		j, err := e.Join(ctx, core.JoinArgs{Team: team.ID, Role: "lead", Name: name, Cwd: team.RootCwd})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := e.Authenticate(ctx, j.ID, j.Token)
		return c
	}
	alice, bob := join("alice"), join("bob")
	if _, err := e.Agent(ctx, alice, core.AgentArgs{Action: core.AgentSpawn, Template: "tf", Task: "review it"}); err != nil {
		t.Fatal(err)
	}
	do := func(c core.Caller, ev, prompt string) {
		t.Helper()
		if _, err := e.HarnessEvent(ctx, c, core.HarnessEventArgs{Event: ev, PromptID: prompt, Outcome: core.HarnessOutcomeOK}); err != nil {
			t.Fatal(err)
		}
	}
	send := func(from core.Caller, to string) {
		t.Helper()
		if _, err := e.Send(ctx, from, core.SendArgs{To: to, Body: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	// the gate's last send of a turn on mail, bob idle: only the taskforce is left
	gateTurn := func(prompt, to string) {
		send(bob, "alice")
		do(alice, core.HarnessTurnStart, prompt)
		send(alice, to)
		if to == "bob" {
			do(bob, core.HarnessTurnStart, prompt+"b")
			do(bob, core.HarnessTurnEnd, prompt+"b")
		}
		do(alice, core.HarnessTurnEnd, prompt)
	}
	gateTurn("a1", "tf")
	if len(sunk) != 0 {
		t.Fatalf("settled while its taskforce has the work: %v", sunk)
	}
	if _, err := e.Agent(ctx, alice, core.AgentArgs{Action: core.AgentClose, Team: "tf"}); err != nil {
		t.Fatal(err)
	}
	gateTurn("a2", "bob")
	if len(sunk) != 1 {
		t.Fatalf("after the close: %d notices, want settled", len(sunk))
	}
}
