package core_test

import (
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
	"github.com/sting8k/piggery/manifests"
)

// Exercise the shipped template through unpack, spawn and mail delivery: reviewers can
// hand back to the Lead but cannot leak findings to one another or the board.
func TestDualLaneAdjudicationIsolation(t *testing.T) {
	home := t.TempDir()
	if err := manifests.Unpack(home); err != nil {
		t.Fatal(err)
	}
	manifest, err := manifests.Resolve("dual-lane-adjudication", home)
	if err != nil {
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
	e := core.New(db, core.WithRuntime(codex), core.WithRuntime(claude), core.WithDefaultHarness("claude"))
	team, err := e.TeamUp(ctx, core.TeamUpArgs{Manifest: manifest, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Join(ctx, core.JoinArgs{Team: team.ID, Role: "lead", Name: "lead", Cwd: team.RootCwd})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := e.Authenticate(ctx, j.ID, j.Token)
	if err != nil {
		t.Fatal(err)
	}
	reviewers := map[string]core.Caller{}
	for _, role := range []string{"lane_a", "lane_b"} {
		res, err := e.Agent(ctx, lead, core.AgentArgs{Action: core.AgentSpawn, Role: role, Name: role, Task: "Readiness only"})
		if err != nil {
			t.Fatalf("spawn %s: %v", role, err)
		}
		rt := codex
		wantModel := "gpt-6-astra"
		if role == "lane_b" {
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
	if _, err := e.Agent(ctx, lead, core.AgentArgs{Action: core.AgentSpawn, Role: "lane_a", Name: "extra", Task: "t"}); rule(err) != "limit/limits.concurrency" {
		t.Fatalf("third live worker: %v; want concurrency denied", err)
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
		if _, err := e.Agent(ctx, c, core.AgentArgs{Action: core.AgentSpawn, Role: "lane_a", Name: "child", Task: "t"}); rule(err) != "permission/tools.not_granted" {
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
	if err != nil || len(mail) != 2 {
		t.Fatalf("Lead inbox: %+v, %v; want both handbacks", mail, err)
	}
	for _, m := range mail {
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
}
