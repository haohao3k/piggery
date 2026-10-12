package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/proto"
	"github.com/sting8k/piggery/manifests"
)

func TestAgentInstructionsSurviveRoleChanges(t *testing.T) {
	dir := startServer(t)
	source, err := os.ReadFile("../../manifests/prompts/agent-ownership.md")
	if err != nil {
		t.Fatal(err)
	}
	common := strings.TrimSpace(string(source))
	if common == "" {
		t.Fatal("common agent instructions are empty")
	}
	customDir := filepath.Join(manifests.Dir(dir), "custom")
	if err := os.MkdirAll(customDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customDir, manifests.ManifestFile), []byte(`template: custom
summary: An independent advisory role.
auto_join_role: advisor
roles:
  advisor:
    tools: [send, inbox, who, agent]
    instructions: Give an evidence-backed opinion without changing files.
`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := dial(t, dir)
	var solo core.JoinResult
	if _, err := c.CallInto(proto.VerbJoinAuto, core.JoinAutoArgs{Cwd: dir, Harness: "pi", Mode: "rpc", HarnessRef: "ownership-session"}, &solo); err != nil {
		t.Fatal(err)
	}
	c.AsParticipant(solo.ID, solo.Token)
	var run string
	card := func(wantRole string) string {
		t.Helper()
		var id core.IdentifyResult
		if _, err := c.CallInto(proto.VerbIdentify, core.IdentifyArgs{NewRun: run == "", RunID: run, Again: run != "", Harness: "pi"}, &id); err != nil {
			t.Fatal(err)
		}
		run = id.RunID
		if id.Role != wantRole || strings.Count(id.RoleCard, common) != 1 {
			t.Fatalf("role %q: want role %q and the common instructions exactly once; got %q", id.Role, wantRole, id.RoleCard)
		}
		return id.RoleCard
	}
	card("")
	if _, err := c.Call(proto.VerbAgent, core.AgentArgs{Action: core.AgentFound, Template: "slp"}); err != nil {
		t.Fatal(err)
	}
	if got := card("supervisor"); !strings.Contains(got, "You are the Supervisor.") {
		t.Fatal("built-in role instructions were lost")
	}
	var next core.AgentResult
	if _, err := c.CallInto(proto.VerbAgent, core.AgentArgs{Action: core.AgentFound, Template: "custom"}, &next); err != nil {
		t.Fatal(err)
	}
	c = dial(t, dir)
	c.AsParticipant(next.ParticipantID, next.Token)
	run = next.RunID
	if got := card("advisor"); !strings.Contains(got, "Give an evidence-backed opinion without changing files.") {
		t.Fatal("custom role instructions were lost")
	}
}
