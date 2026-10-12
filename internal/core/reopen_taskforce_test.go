package core_test

import (
	"path/filepath"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
	"github.com/sting8k/piggery/manifests"
)

func TestReopenByTheOldGatesSessionKeepsItsTaskforce(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p2p, err := manifests.Builtin("lead-peer")
	if err != nil {
		t.Fatal(err)
	}
	const tf = "template: tf\ntaskforce: {idle_for: 20m}\nauto_join_role: chair\nroles:\n  chair: {tools: [send, inbox, who, agent]}\n"
	rt := &fakeRuntime{}
	e := core.New(db, core.WithRuntime(rt), core.WithTemplates(func(name, _ string) (string, error) {
		if name == "tf" {
			return tf, nil
		}
		return p2p, nil
	}))
	root, _ := filepath.EvalSymlinks(t.TempDir())
	session := func() core.Caller {
		j, err := e.JoinAuto(ctx, core.JoinAutoArgs{Cwd: root, Harness: "pi", Mode: "rpc", HarnessRef: "S1"})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := e.Authenticate(ctx, j.ID, j.Token)
		return c
	}
	agent := func(c core.Caller, a core.AgentArgs) core.AgentResult {
		r, err := e.Agent(ctx, c, a)
		if err != nil {
			t.Fatalf("%s: %v", a.Action, err)
		}
		return r
	}
	team := agent(session(), core.AgentArgs{Action: core.AgentFound, Template: "lead-peer"})
	if _, err := e.TeamDown(ctx, core.TeamDownArgs{Team: team.TeamID}); err != nil {
		t.Fatal(err)
	}
	solo := session()
	taskforce := agent(solo, core.AgentArgs{Action: core.AgentSpawn, Template: "tf", Task: "review it"})
	gate := agent(solo, core.AgentArgs{Action: core.AgentReopen, Team: team.TeamName})
	if _, err := e.Taskforces(ctx); err != nil {
		t.Fatal(err)
	}
	var closed, gateName string
	db.QueryRow(`SELECT COALESCE(closed_at,'') FROM teams WHERE id=?`, taskforce.TeamID).Scan(&closed)
	if closed != "" {
		t.Fatalf("the taskforce closed when the session that called it reopened its old team (%d workers stopped)", len(rt.stopped))
	}
	db.QueryRow(`SELECT name FROM participants WHERE id=?`, gate.ParticipantID).Scan(&gateName)
	chair, _ := e.Authenticate(ctx, rt.Starts()[0].ParticipantID, rt.Starts()[0].Token)
	if _, err := e.Send(ctx, chair, core.SendArgs{To: gateName, Body: "result"}); err != nil {
		t.Fatalf("the chair cannot answer its caller after the reopen: %v", err)
	}
}
