package core_test

import (
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
	"github.com/sting8k/piggery/manifests"
)

// Exercise the shipped template through unpack, spawn and mail delivery: reviewers can
// hand back to the Lead but cannot leak findings to one another or the board.
func TestDualLensTaskforceIsolation(t *testing.T) {
	home := t.TempDir()
	if err := manifests.Unpack(home); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	codex := &fakeRuntime{harness: "codex", profileModel: "test-openai", profileThinking: "low"}
	claude := &fakeRuntime{harness: "claude", profileModel: "test-anthropic", profileThinking: "low"}
	// Deliberately different profile models/effort and default harness must not override pins.
	e := core.New(db, core.WithRuntime(codex), core.WithRuntime(claude), core.WithDefaultHarness("claude"),
		core.WithTemplates(func(name, _ string) (string, error) { return manifests.Resolve(name, home) }))
	j, err := e.JoinAuto(ctx, core.JoinAutoArgs{Cwd: t.TempDir(), Harness: "codex", Mode: "interactive", HarnessRef: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := e.Authenticate(ctx, j.ID, j.Token)
	if err != nil {
		t.Fatal(err)
	}
	taskforce, err := e.Agent(ctx, caller, core.AgentArgs{Action: core.AgentSpawn, Template: "dual-lens", Task: "Review the frozen candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.starts) != 1 {
		t.Fatalf("chair starts: %d", len(codex.starts))
	}
	chair := codex.starts[0]
	lead, err := e.Authenticate(ctx, chair.ParticipantID, chair.Token)
	if err != nil {
		t.Fatal(err)
	}
	reviewers := map[string]core.Caller{}
	for _, role := range []string{"lens-a", "lens-b"} {
		res, err := e.Agent(ctx, lead, core.AgentArgs{Action: core.AgentSpawn, Role: role, Name: role, Task: "Readiness only"})
		if err != nil {
			t.Fatalf("spawn %s: %v", role, err)
		}
		rt := codex
		wantModel := "gpt-6-astra"
		if role == "lens-b" {
			rt = claude
			wantModel = "claude-opus-5-5"
		}
		spec := rt.starts[len(rt.starts)-1]
		if spec.ParticipantID != res.ParticipantID || spec.Model != wantModel {
			t.Fatalf("%s route: %+v", role, spec)
		}
		if spec.Thinking != "high" {
			t.Fatalf("%s effort = %q, want high", role, spec.Thinking)
		}
		c, err := e.Authenticate(ctx, spec.ParticipantID, spec.Token)
		if err != nil {
			t.Fatal(err)
		}
		reviewers[role] = c
		if _, err := e.Send(ctx, lead, core.SendArgs{To: role, Kind: "review", Body: "The same neutral brief"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Agent(ctx, lead, core.AgentArgs{Action: core.AgentSpawn, Role: "lens-a", Name: "extra", Task: "t"}); rule(err) != "limit/limits.concurrency" {
		t.Fatalf("fourth live worker: %v; want concurrency denied", err)
	}
	for name, c := range reviewers {
		for other := range reviewers {
			if name == other {
				continue
			}
			if _, err := e.Send(ctx, c, core.SendArgs{To: other, Body: "leaked finding"}); code(err) != core.CodeDenied {
				t.Fatalf("%s -> %s: %v; want denied", name, other, err)
			}
		}
		for _, to := range []string{core.AddrBoard, core.AddrNotify} {
			if _, err := e.Send(ctx, c, core.SendArgs{To: to, Body: "leaked finding"}); code(err) != core.CodeDenied {
				t.Fatalf("%s -> %s: %v; want denied", name, to, err)
			}
		}
		if _, err := e.Agent(ctx, c, core.AgentArgs{Action: core.AgentSpawn, Role: "lens-a", Name: "child", Task: "t"}); rule(err) != "permission/tools.not_granted" {
			t.Fatalf("%s spawn: %v; want denied", name, err)
		}
		if _, err := e.Send(ctx, c, core.SendArgs{To: "lead", Kind: "handback", Body: "private " + name}); err != nil {
			t.Fatalf("%s handback: %v", name, err)
		}
	}
	for name, c := range reviewers {
		mail, err := e.Inbox(ctx, c, core.InboxArgs{})
		if err != nil || len(mail) != 2 { // own readiness assignment and review; no copies
			t.Fatalf("%s inbox: %+v, %v; want only its two assignments", name, mail, err)
		}
	}
	mail, err := e.Inbox(ctx, lead, core.InboxArgs{})
	if err != nil || len(mail) != 3 {
		t.Fatalf("Lead inbox: %+v, %v; want task plus both handbacks", mail, err)
	}
	for _, m := range mail[1:] {
		if m.Kind != "handback" {
			t.Fatalf("Lead received unexpected mail: %+v", m)
		}
	}

	// Conflicts are relayed through the Lead; first-pass isolation stays enforced.
	for role, c := range reviewers {
		if _, err := e.Send(ctx, lead, core.SendArgs{To: role, Kind: "follow", Body: "C1: both claims and the opposing evidence"}); err != nil {
			t.Fatal(err)
		}
		inbox, err := e.Inbox(ctx, c, core.InboxArgs{})
		if err != nil || len(inbox) != 3 || inbox[2].Kind != "follow" {
			t.Fatalf("conflict not delivered to %s: %+v %v", role, inbox, err)
		}
		if _, err := e.Send(ctx, c, core.SendArgs{To: "lead", Kind: "handback", Body: "C1: REVISE with deciding evidence"}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := e.Send(ctx, lead, core.SendArgs{To: caller.ParticipantID, Kind: "answer", Body: "DECIDED_WITH_DISSENT"}); err != nil {
		t.Fatalf("chair cannot return the result to its caller: %v", err)
	}
	answers, err := e.Inbox(ctx, caller, core.InboxArgs{})
	if err != nil || len(answers) != 1 || answers[0].Body != "DECIDED_WITH_DISSENT" {
		t.Fatalf("caller handback: %+v, %v", answers, err)
	}
	if _, err := e.Agent(ctx, caller, core.AgentArgs{Action: core.AgentClose, Team: taskforce.TeamName}); err != nil {
		t.Fatalf("caller cannot close its taskforce: %v", err)
	}
}
