package core_test

import (
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
	"github.com/sting8k/piggery/manifests"
)

// Exercise the shipped template through unpack, spawn and mail delivery: reviewers can
// hand back to the coordinator but cannot leak findings to one another or the board.
func TestTripleReviewIsolation(t *testing.T) {
	home := t.TempDir()
	if err := manifests.Unpack(home); err != nil {
		t.Fatal(err)
	}
	manifest, err := manifests.Resolve("triple-review", home)
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
	e := core.New(db, core.WithRuntime(codex), core.WithRuntime(claude), core.WithDefaultHarness("codex"))
	team, err := e.TeamUp(ctx, core.TeamUpArgs{Manifest: manifest, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Join(ctx, core.JoinArgs{Team: team.ID, Role: "coordinator", Name: "lead", Cwd: team.RootCwd})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := e.Authenticate(ctx, j.ID, j.Token)
	if err != nil {
		t.Fatal(err)
	}
	reviewers := map[string]core.Caller{}
	for _, role := range []string{"semantic_a", "semantic_b", "coverage"} {
		res, err := e.Agent(ctx, lead, core.AgentArgs{Action: core.AgentSpawn, Role: role, Name: role, Task: "Readiness only"})
		if err != nil {
			t.Fatalf("spawn %s: %v", role, err)
		}
		rt := codex
		if role == "semantic_b" {
			rt = claude
		}
		spec := rt.starts[len(rt.starts)-1]
		if spec.ParticipantID != res.ParticipantID || spec.Model != rt.profileModel {
			t.Fatalf("%s route: %+v", role, spec)
		}
		if role != "coverage" && spec.Thinking != "high" {
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
	if _, err := e.Agent(ctx, lead, core.AgentArgs{Action: core.AgentSpawn, Role: "coverage", Name: "extra", Task: "t"}); rule(err) != "limit/limits.concurrency" {
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
		if _, err := e.Agent(ctx, c, core.AgentArgs{Action: core.AgentSpawn, Role: "coverage", Name: "child", Task: "t"}); rule(err) != "permission/tools.not_granted" {
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
		t.Fatalf("coordinator inbox: %+v, %v; want all three handbacks", mail, err)
	}
	for _, m := range mail {
		if m.Kind != "handback" {
			t.Fatalf("coordinator received unexpected mail: %+v", m)
		}
	}
}
