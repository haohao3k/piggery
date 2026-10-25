package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/internal/proto"
)

// gc cuts the run logs of a worker gone for long (local.TrimLogs) and top must show the same for
// it afterwards: turns over every run, and the context of the current run, which here is only in
// a record that goes. top's cache still points past the end of the shorter file.
func TestTrimmedLogsShowTheSameUsageInTop(t *testing.T) {
	dir := t.TempDir()
	ps := proto.PsResult{State: core.State{Teams: []core.TeamState{{Members: []core.MemberState{
		{ID: "w1", RunID: "r2", Headless: true, Capabilities: []string{core.CapUsage}}}}}}}
	old, cur := local.LogPath(dir, "w1", "r1"), local.LogPath(dir, "w1", "r2")
	if err := os.MkdirAll(filepath.Dir(cur), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":4242}}}` + "\n")
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&b, "{\"type\":\"tool_execution_start\",\"toolName\":\"bash\",\"n\":%d}\n", i)
		if i%100 == 0 {
			b.WriteString(`{"type":"turn_end"}` + "\n")
		}
	}
	files := map[string]string{
		old: `{"type":"turn_end"}` + "\n" + `{"type":"turn_end"}` + "\n",
		strings.TrimSuffix(old, ".jsonl") + ".stderr": "old\n",
		cur: b.String(),
		strings.TrimSuffix(cur, ".jsonl") + ".stderr": strings.Repeat("a line of stderr\n", 20000),
	}
	for path, s := range files {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := &topModel{dir: dir, ps: ps}
	m.logs = readLogs(dir, ps, nil)
	before := m.stats()["w1"]
	if before.Ctx != 4242 || before.Turns != 17 {
		t.Fatalf("before = %+v; want ctx 4242, turns 17 (2 + 15)", before)
	}

	freed, err := local.TrimLogs(dir, "w1", "r2", false)
	if err != nil || freed <= 0 {
		t.Fatalf("TrimLogs = %d, %v", freed, err)
	}
	m.logs = readLogs(dir, ps, m.logs)
	if after := m.stats()["w1"]; after != before {
		t.Fatalf("after the trim top shows %+v; it showed %+v", after, before)
	}

	for _, gone := range []string{old, strings.TrimSuffix(old, ".jsonl") + ".stderr"} {
		if _, err := os.Stat(gone); err == nil {
			t.Fatalf("%s kept", gone)
		}
	}
	kept, _ := os.ReadFile(cur)
	lines := strings.Split(strings.TrimRight(string(kept), "\n"), "\n")
	if len(lines) != 1001 || !strings.Contains(lines[0], "piggery_trimmed") || lines[1000] != fmt.Sprintf(`{"type":"tool_execution_start","toolName":"bash","n":%d}`, 1499) {
		t.Fatalf("kept log: %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	if fi, _ := os.Stat(strings.TrimSuffix(cur, ".jsonl") + ".stderr"); fi.Size() > 64<<10 {
		t.Fatalf("stderr kept %d bytes", fi.Size())
	}
	if again, err := local.TrimLogs(dir, "w1", "r2", false); err != nil || again != 0 {
		t.Fatalf("a log already cut is cut again: %d, %v", again, err)
	}
}
