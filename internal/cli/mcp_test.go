package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	dshext "github.com/sting8k/piggery/extensions/dsh"
	ompext "github.com/sting8k/piggery/extensions/omp"
	piext "github.com/sting8k/piggery/extensions/pi"
	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/proto"
	"github.com/sting8k/piggery/internal/server"
	"gopkg.in/yaml.v3"
)

// fakeDaemon answers requests on dir's socket with answer(verb, args) and can push frames to
// the last connection that identified.
type fakeDaemon struct {
	mu     sync.Mutex
	calls  []proto.Request
	ident  net.Conn
	answer func(verb string, args json.RawMessage) any
}

func startFakeDaemon(t *testing.T, dir string, answer func(string, json.RawMessage) any) *fakeDaemon {
	t.Helper()
	ln, err := net.Listen("unix", server.SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	d := &fakeDaemon{answer: answer}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var req proto.Request
					json.Unmarshal(sc.Bytes(), &req)
					d.mu.Lock()
					d.calls = append(d.calls, req)
					if req.Verb == proto.VerbIdentify {
						d.ident = c
					}
					d.mu.Unlock()
					v := d.answer(req.Verb, req.Args)
					resp := proto.Response{ID: req.ID, OK: true}
					if ce, ok := v.(*core.Error); ok {
						resp.OK, resp.Error = false, ce
					} else {
						resp.Result, _ = json.Marshal(v)
					}
					line, _ := json.Marshal(resp)
					d.mu.Lock()
					c.Write(append(line, '\n'))
					d.mu.Unlock()
				}
			}()
		}
	}()
	return d
}

func (d *fakeDaemon) push(t *testing.T, ev string) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	b, _ := json.Marshal(proto.Push{Event: ev})
	if _, err := d.ident.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(end) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// piggery mcp against a fake daemon and a fake Claude inbox socket: it identifies with the
// worker's run, lists the role's tools, calls a tool over the same connection that carries
// pushes, turns a wake push into one nudge line (auth line first) and a role push into a new
// identify plus tools/list_changed.
func TestMCP(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgmcp") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	tools := []string{"send", "who"}
	var mu sync.Mutex
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		mu.Lock()
		defer mu.Unlock()
		switch verb {
		case proto.VerbIdentify:
			return core.IdentifyResult{ParticipantID: "P", RunID: "R", Tools: tools}
		case proto.VerbSend:
			return core.SendResult{Seq: 7}
		}
		return map[string]any{}
	})
	inbox, err := net.Listen("unix", filepath.Join(dir, "cc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	nudges := make(chan string, 4)
	go func() {
		for {
			c, err := inbox.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(c)
			nudges <- string(b)
		}
	}()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &mcpServer{dir: dir, id: "P", token: "T", run: "R", ref: "sess-1", sock: filepath.Join(dir, "cc.sock"),
		sockTk: "tk", out: outW, ready: make(chan struct{})}
	s.startKeepConnected()
	go s.serve(inR)
	out := bufio.NewScanner(outR)
	rpc := func(id int, method string, params any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		inW.Write(append(b, '\n'))
		for out.Scan() {
			var m map[string]any
			json.Unmarshal(out.Bytes(), &m)
			if m["id"] == float64(id) {
				return m
			}
		}
		t.Fatalf("no answer to %s", method)
		return nil
	}

	list := rpc(1, "tools/list", map[string]any{})
	var names []string
	for _, x := range list["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "send,who" {
		t.Fatalf("tools = %v", names)
	}
	var id core.IdentifyArgs
	json.Unmarshal(d.calls[0].Args, &id)
	if id.NewRun || id.RunID != "R" || id.ToolPrefix != mcpPrefix || id.HarnessRef != "sess-1" {
		t.Fatalf("identify = %+v", id)
	}
	sent := rpc(2, "tools/call", map[string]any{"name": "send", "arguments": map[string]any{"to": "lead", "body": "hi"}})
	if txt := sent["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]; txt != "sent #7" {
		t.Fatalf("send = %v", sent)
	}

	d.push(t, proto.EventWake)
	select {
	case n := <-nudges:
		lines := strings.Split(strings.TrimSpace(n), "\n")
		if len(lines) != 2 || lines[0] != `{"token":"tk","type":"auth"}` || !strings.Contains(lines[1], `"type":"user"`) ||
			!strings.Contains(lines[1], "wake #1") || strings.Contains(lines[1], "priority") {
			t.Fatalf("nudge = %q", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no nudge after a wake push")
	}

	mu.Lock()
	tools = []string{"send", "who", "inbox"}
	mu.Unlock()
	d.push(t, proto.EventRole)
	for out.Scan() {
		if strings.Contains(out.Text(), "notifications/tools/list_changed") {
			break
		}
	}
	inW.Close()
}

func TestMCPThreadIDMetadata(t *testing.T) {
	tests := []struct {
		name string
		meta string
		want string
		err  bool
	}{
		{name: "codex", meta: `{"threadId":"t1","source":"codex"}`, want: "t1"},
		{name: "legacy snake case", meta: `{"thread_id":"t2"}`, want: "t2"},
		{name: "absent", meta: `{}`},
		{name: "non object", meta: `"bad"`, err: true},
		{name: "non string", meta: `{"threadId":7}`, err: true},
		{name: "conflict", meta: `{"threadId":"t1","thread_id":"t2"}`, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mcpThreadID(json.RawMessage(tt.meta))
			if (err != nil) != tt.err || got != tt.want {
				t.Fatalf("mcpThreadID(%s) = %q, %v; want %q, error=%v", tt.meta, got, err, tt.want, tt.err)
			}
		})
	}
}

func TestRequireSessionAuthFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer any
	}{
		{name: "old daemon", answer: &core.Error{Code: core.CodeInvalid, Message: "unknown verb " + proto.VerbSessionSupport}},
		{name: "capability absent", answer: proto.SessionSupportResult{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "pgsupport")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			startFakeDaemon(t, dir, func(string, json.RawMessage) any { return tc.answer })
			c, err := Dial(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			err = requireSessionAuth(c)
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != core.CodeUnsupported || ce.RuleID != "session.unsupported" ||
				!strings.Contains(ce.Message, "upgrade the Piggery daemon and integration") {
				t.Fatalf("requireSessionAuth = %v, want session.unsupported", err)
			}
		})
	}
}

func TestMCPCodexInitializeRequestsBinding(t *testing.T) {
	s := &mcpServer{host: "codex:9:1", harness: "codex", out: io.Discard, ready: make(chan struct{})}
	res, rerr := s.handle(rpcMsg{Method: "initialize", Params: json.RawMessage(`{}`)})
	if rerr != nil {
		t.Fatal(rerr)
	}
	instructions := res.(map[string]any)["instructions"].(string)
	if !strings.Contains(instructions, mcpPrefix+"who") || !strings.Contains(instructions, "cannot route mail or wakes") {
		t.Fatalf("unbound Codex instructions = %q", instructions)
	}
}

// piggery mcp in a Claude session the Human opened (no PIGGERY_*): it places the session with
// join.auto by exact ref plus host lineage, then authenticates the same binding; it declares wake
// and steer only, and hands the role card and mail channel to Claude as server instructions.
func TestMCPSession(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgmcps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		switch verb {
		case proto.VerbJoinAuto:
			return core.JoinResult{ID: "P", RunID: "R"}
		case proto.VerbIdentify:
			return core.IdentifyResult{ParticipantID: "P", RunID: "R", RoleCard: "CARD", Name: "gopher", Role: "lead",
				Tools: []string{"send", "who", "agent"}}
		case proto.VerbAgent:
			return core.AgentResult{TeamName: "t1"}
		}
		return map[string]any{}
	})
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &mcpServer{dir: dir, host: "claude:9:1", ref: "sess-1", out: outW, ready: make(chan struct{})}
	s.startKeepConnected()
	go s.serve(inR)
	out := bufio.NewScanner(outR)
	inW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"))
	out.Scan()
	var init struct {
		Result struct{ Instructions string } `json:"result"`
	}
	json.Unmarshal(out.Bytes(), &init)
	// The fake daemon sends no protocol_version, like one older than this adapter: the session is told.
	if !strings.HasPrefix(init.Result.Instructions, "CARD") || !strings.Contains(init.Result.Instructions, mcpPrefix+"inbox") ||
		!strings.Contains(init.Result.Instructions, "piggery restart") {
		t.Fatalf("instructions %q", init.Result.Instructions)
	}
	d.mu.Lock()
	calls := d.calls
	d.mu.Unlock()
	var j core.JoinAutoArgs
	json.Unmarshal(calls[0].Args, &j)
	var id core.IdentifyArgs
	json.Unmarshal(calls[1].Args, &id)
	if calls[0].Verb != proto.VerbJoinAuto || j.Host != "claude:9:1" || j.HarnessRef != "sess-1" || j.Mode != "interactive" ||
		calls[1].Verb != proto.VerbIdentify || calls[1].Auth == nil || calls[1].Auth.Host != "claude:9:1" || calls[1].Auth.HarnessRef != "sess-1" || calls[1].Auth.Token != "" ||
		strings.Join(id.Capabilities, ",") != "wake,steer" || id.ProtocolVersion != core.ProtocolVersion {
		t.Fatalf("calls %s %+v, %s %+v %+v", calls[0].Verb, j, calls[1].Verb, calls[1].Auth, id)
	}

	// found: the session is another participant now; it identifies again by the
	// same exact session binding and says so.
	inW.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"agent","arguments":{"action":"found","template":"x"}}}` + "\n"))
	out.Scan()
	if !strings.Contains(out.Text(), "founded team t1; you are gopher (lead), its gate") {
		t.Fatalf("found = %s", out.Text())
	}
	d.mu.Lock()
	last := d.calls[len(d.calls)-1]
	d.mu.Unlock()
	if last.Verb != proto.VerbIdentify || last.Auth.Host != "claude:9:1" {
		t.Fatalf("after found: %s %+v", last.Verb, last.Auth)
	}

	// spawn with cwd: the worker's directory reaches the agent verb as the model wrote it.
	inW.Write([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent","arguments":{"action":"spawn","role":"lead","name":"l1","task":"t","cwd":"../lane-a"}}}` + "\n"))
	out.Scan()
	d.mu.Lock()
	last = d.calls[len(d.calls)-1]
	d.mu.Unlock()
	var sp core.AgentArgs
	json.Unmarshal(last.Args, &sp)
	if last.Verb != proto.VerbAgent || sp.Action != core.AgentSpawn || sp.Cwd != "../lane-a" {
		t.Fatalf("spawn: %s %+v", last.Verb, sp)
	}
	inW.Close()
}

// piggery hook claude: Stop with mail left maps to turn_end ok with the prompt id and prints a
// Stop block; with no PIGGERY_* identity it calls nothing and prints nothing.
func TestHookClaude(t *testing.T) {
	dir, err := os.MkdirTemp("", "pghook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(string, json.RawMessage) any {
		return core.HarnessEventResult{Text: "[piggery] 1 new message", Block: true}
	})
	var out bytes.Buffer
	e := &env{dir: dir, stdout: &out}
	stdin := `{"session_id":"s","prompt_id":"p1","stop_hook_active":true,"hook_event_name":"Stop"}`

	t.Setenv("PIGGERY_ID", "")
	e.runHook("claude", "Stop", strings.NewReader(stdin), io.Discard)
	if out.Len() != 0 || len(d.calls) != 0 {
		t.Fatalf("without identity: out %q, calls %d", out.String(), len(d.calls))
	}

	t.Setenv("PIGGERY_ID", "P")
	t.Setenv("PIGGERY_TOKEN", "T")
	e.runHook("claude", "Stop", strings.NewReader(stdin), io.Discard)
	var a core.HarnessEventArgs
	json.Unmarshal(d.calls[0].Args, &a)
	if d.calls[0].Verb != proto.VerbHarnessEvent || a.Event != core.HarnessTurnEnd || a.Outcome != core.HarnessOutcomeOK ||
		a.PromptID != "p1" || !a.StopHookActive {
		t.Fatalf("harness.event = %s %+v", d.calls[0].Verb, a)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got["decision"] != "block" || got["reason"] != "[piggery] 1 new message" {
		t.Fatalf("hook output %q", out.String())
	}
}

// A session the Human opened: SessionStart places it by host (join.auto) with its transcript,
// then the event goes out as that host; idle_prompt is the idle event; piggery's own tools are allowed without a
// daemon call, any other tool is left to Claude.
func TestHookClaudeSession(t *testing.T) {
	dir, err := os.MkdirTemp("", "pghooks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(string, json.RawMessage) any { return map[string]any{} })
	t.Setenv("PIGGERY_ID", "")
	t.Setenv("PIGGERY_TOKEN", "")
	t.Setenv("PIGGERY_DISABLED", "")
	old := sessionHost
	sessionHost = func(...string) string { return "claude:9:1" }
	t.Cleanup(func() { sessionHost = old })
	var out bytes.Buffer
	e := &env{dir: dir, stdout: &out}

	e.runHook("claude", "SessionEnd", strings.NewReader(`{"session_id":"s1","reason":"clear"}`), io.Discard) // not the end of the session
	e.runHook("claude", "SessionStart", strings.NewReader(`{"session_id":"s2","source":"clear","cwd":"/w","transcript_path":"/p/s2.jsonl"}`), io.Discard)
	e.runHook("claude", "Notification", strings.NewReader(`{"session_id":"s2","notification_type":"idle_prompt"}`), io.Discard)
	e.runHook("claude", "Notification", strings.NewReader(`{"session_id":"s2","notification_type":"permission_prompt"}`), io.Discard)
	var j core.JoinAutoArgs
	json.Unmarshal(d.calls[0].Args, &j)
	var start, idle core.HarnessEventArgs
	json.Unmarshal(d.calls[1].Args, &start)
	json.Unmarshal(d.calls[2].Args, &idle)
	if len(d.calls) != 3 || d.calls[0].Verb != proto.VerbJoinAuto || j.Host != "claude:9:1" || j.Source != "clear" || j.HarnessRef != "s2" ||
		j.Cwd != "/w" || j.Transcript == nil || *j.Transcript != (core.Transcript{Path: "/p/s2.jsonl", Format: "claude"}) || d.calls[1].Auth.Host != "claude:9:1" || d.calls[1].Auth.HarnessRef != "s2" || d.calls[1].Auth.Cwd != "/w" || start.Event != core.HarnessSessionStart || start.Source != "clear" || idle.Event != core.HarnessIdle {
		t.Fatalf("calls %d: %+v %+v %+v", len(d.calls), j, start, idle)
	}
	if out.Len() != 0 {
		t.Fatalf("output %q", out.String())
	}

	e.runHook("claude", "PreToolUse", strings.NewReader(`{"tool_name":"mcp__piggery__send"}`), io.Discard)
	e.runHook("claude", "PreToolUse", strings.NewReader(`{"tool_name":"Bash"}`), io.Discard)
	if len(d.calls) != 3 || strings.Count(out.String(), `"permissionDecision":"allow"`) != 1 {
		t.Fatalf("PreToolUse: calls %d, out %q", len(d.calls), out.String())
	}
}

// piggery hook codex in a TUI session: SessionStart joins by the exact thread ref with its source;
// the turn key is turn_id; Stop with no mail still answers JSON; Interrupt ends the turn unacked; a
// native subagent's hook (agent_id) is dropped; SessionEnd names its session id. An ephemeral
// thread's null transcript_path sends no transcript.
func TestHookCodexSession(t *testing.T) {
	dir, err := os.MkdirTemp("", "pghookx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbSessionSupport {
			return proto.SessionSupportResult{ThreadAuth: true}
		}
		return map[string]any{}
	})
	t.Setenv("PIGGERY_ID", "")
	t.Setenv("PIGGERY_TOKEN", "")
	t.Setenv("PIGGERY_DISABLED", "")
	old := sessionHost
	sessionHost = func(...string) string { return "codex:9:1" }
	t.Cleanup(func() { sessionHost = old })
	var out bytes.Buffer
	e := &env{dir: dir, stdout: &out}
	run := func(hook, in string) { e.runHook("codex", hook, strings.NewReader(in), io.Discard) }

	run("SessionStart", `{"session_id":"s1","source":"startup","cwd":"/w","transcript_path":null}`)
	run("UserPromptSubmit", `{"session_id":"s1","turn_id":"t1"}`)
	run("PostToolUse", `{"session_id":"s1","turn_id":"t9","agent_id":"a1"}`) // a subagent's
	run("Stop", `{"session_id":"s1","turn_id":"t1","stop_hook_active":false}`)
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("Stop output %q, want {}", out.String())
	}
	run("Interrupt", `{"session_id":"s1","turn_id":"t2"}`)
	run("SessionEnd", `{"session_id":"s0","reason":"other"}`)
	var j core.JoinAutoArgs
	var evs []core.HarnessEventArgs
	var eventAuth *proto.Auth
	for _, c := range d.calls {
		if c.Verb == proto.VerbSessionSupport {
			continue
		}
		if c.Verb == proto.VerbJoinAuto {
			json.Unmarshal(c.Args, &j)
			continue
		}
		var a core.HarnessEventArgs
		json.Unmarshal(c.Args, &a)
		evs = append(evs, a)
		if eventAuth == nil {
			eventAuth = c.Auth
		}
	}
	if j.Harness != "codex" || j.Host != "codex:9:1" || j.Transcript != nil || j.Source != "startup" || j.HarnessRef != "s1" || eventAuth == nil || eventAuth.HarnessRef != "s1" || len(evs) != 5 ||
		evs[1].Event != core.HarnessTurnStart || evs[1].PromptID != "t1" ||
		evs[2].Event != core.HarnessTurnEnd || evs[2].Outcome != core.HarnessOutcomeOK || evs[2].PromptID != "t1" ||
		evs[3].Outcome != core.HarnessOutcomeIntr || evs[3].PromptID != "t2" ||
		evs[4].Event != core.HarnessSessionEnd || evs[4].HarnessRef != "s0" {
		t.Fatalf("join %+v, events %+v", j, evs)
	}
}

// piggery mcp in a Codex TUI wakes it with `codex queue --thread <the ref the wake names>`.
func TestMCPCodexWakeQueues(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgmcpx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	args := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "codex")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" > "+args+"\n"), 0o755)
	old := codexBin
	codexBin = func() string { return fake }
	t.Cleanup(func() { codexBin = old })
	s := &mcpServer{host: "codex:9:1", harness: "codex", ref: "thread-2"}
	s.queueNudge("thread-1") // a wake for the old /clear binding must be dropped
	if _, err := os.Stat(args); !os.IsNotExist(err) {
		t.Fatalf("stale wake reached codex: stat err=%v", err)
	}
	s.onPush(proto.Push{Event: proto.EventWake, Ref: "thread-2"})
	b, _ := os.ReadFile(args)
	if !strings.HasPrefix(string(b), "queue --thread thread-2 --message [piggery] wake #1") {
		t.Fatalf("codex called with %q", b)
	}
}

func TestMCPCodexWakeTimeout(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nwhile true; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBin, oldTimeout := codexBin, codexQueueTimeout
	codexBin = func() string { return fake }
	codexQueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		codexBin = oldBin
		codexQueueTimeout = oldTimeout
	})
	s := &mcpServer{host: "codex:9:1", harness: "codex", ref: "thread-2"}
	started := time.Now()
	s.queueNudge("thread-2")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("queue wake ignored timeout: %s", elapsed)
	}
}

func TestHookPartialCredentialsFailClosed(t *testing.T) {
	t.Setenv("PIGGERY_ID", "participant")
	t.Setenv("PIGGERY_TOKEN", "")
	var out, errOut bytes.Buffer
	e := &env{dir: t.TempDir(), stdout: &out}
	e.runHook("codex", "UserPromptSubmit", strings.NewReader(`{"session_id":"s1","turn_id":"t1"}`), &errOut)
	if out.Len() != 0 || !strings.Contains(errOut.String(), "PIGGERY_ID and PIGGERY_TOKEN must be set together") {
		t.Fatalf("partial credentials: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

// Codex does not expose its thread id in the MCP child's environment. Discovery
// remains identity-neutral; the first call must carry _meta.threadId and then
// authenticates the exact ref.
func TestMCPCodexWaitsForTheSession(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgmcpw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	identifyCount := 0
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbSessionSupport {
			return proto.SessionSupportResult{ThreadAuth: true}
		}
		if verb == proto.VerbIdentify {
			identifyCount++
			return core.IdentifyResult{ParticipantID: fmt.Sprintf("P%d", identifyCount), RunID: "R", RoleCard: fmt.Sprintf("CODEX CARD %d", identifyCount), Tools: []string{"send"}}
		}
		if verb == proto.VerbSend {
			return core.SendResult{Seq: 1}
		}
		return core.JoinResult{ID: "P", RunID: "R"}
	})
	s := &mcpServer{dir: dir, host: "codex:9:1", harness: "codex", out: io.Discard, ready: make(chan struct{})}
	if got := s.toolList(); len(got) != len(mcpBaseTools) {
		t.Fatalf("unbound tools/list = %d, want %d", len(got), len(mcpBaseTools))
	}
	s.mu.Lock()
	listed := s.listed
	s.mu.Unlock()
	if !listed {
		t.Fatal("unbound tools/list did not mark the catalog as listed")
	}
	if _, err := s.callTool("send", json.RawMessage(`{"to":"lead","body":"no ref"}`), ""); !errors.Is(err, errSessionIdentity) {
		t.Fatalf("missing metadata error = %v, want %v", err, errSessionIdentity)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "send",
		"arguments": map[string]any{"to": "lead", "body": "hi"},
		"_meta":     map[string]any{"threadId": "thread-1"},
	})
	res, rerr := s.handle(rpcMsg{Method: "tools/call", Params: params})
	if rerr != nil {
		t.Fatalf("bound call = %v", rerr)
	}
	if text := res.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string); !strings.HasPrefix(text, "CODEX CARD 1\n\n") {
		t.Fatalf("first bound result omitted role card: %q", text)
	}
	if _, err := s.callTool("send", json.RawMessage(`{"to":"lead","body":"missing ref after bind"}`), ""); !errors.Is(err, errSessionIdentity) {
		t.Fatalf("missing metadata after bind error = %v, want %v", err, errSessionIdentity)
	}
	params, _ = json.Marshal(map[string]any{
		"name":      "send",
		"arguments": map[string]any{"to": "lead", "body": "after clear"},
		"_meta":     map[string]any{"threadId": "thread-2"},
	})
	res, rerr = s.handle(rpcMsg{Method: "tools/call", Params: params})
	if rerr != nil {
		t.Fatalf("rotated call = %v", rerr)
	}
	if text := res.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string); !strings.HasPrefix(text, "CODEX CARD 2\n\n") {
		t.Fatalf("rotated bound result omitted role card: %q", text)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c.Verb == proto.VerbSessionSupport {
			continue
		}
		if c.Verb == proto.VerbJoinAuto {
			t.Fatalf("Codex MCP must not join.auto: %+v", c)
		}
		if c.Auth == nil || c.Auth.Host != "codex:9:1" || (c.Auth.HarnessRef != "thread-1" && c.Auth.HarnessRef != "thread-2") {
			t.Fatalf("call %s auth %+v", c.Verb, c.Auth)
		}
	}
	last := d.calls[len(d.calls)-1]
	if last.Verb != proto.VerbSend || last.Auth == nil || last.Auth.HarnessRef != "thread-2" {
		t.Fatalf("rotated send auth = %+v", last.Auth)
	}
}

// An old daemon must be rejected before the Codex MCP child can join, call a
// participant verb, or queue a wake by the shared app-server host.
func TestMCPCodexOldDaemonFailsClosedBeforeJoinAndWake(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgold")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	args := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho called > "+args+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBin := codexBin
	codexBin = func() string { return fake }
	t.Cleanup(func() { codexBin = oldBin })
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbSessionSupport {
			return &core.Error{Code: core.CodeInvalid, Message: "unknown verb " + proto.VerbSessionSupport}
		}
		return map[string]any{}
	})
	s := &mcpServer{dir: dir, host: "codex:9:1", harness: "codex", ref: "thread-1", out: io.Discard, ready: make(chan struct{})}
	s.startKeepConnected()
	waitFor(t, "session capability failure", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return isSessionAuthUnsupported(s.identErr)
	})
	if _, err := s.callTool("send", json.RawMessage(`{"to":"lead","body":"must not send"}`), "thread-1"); !isSessionAuthUnsupported(err) {
		t.Fatalf("old daemon tool call = %v, want session.unsupported", err)
	}
	s.onPush(proto.Push{Event: proto.EventWake, Ref: "thread-1"})
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(args); !os.IsNotExist(err) {
		t.Fatalf("old daemon queued a wake: stat err=%v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c.Verb != proto.VerbSessionSupport {
			t.Fatalf("old daemon received mutating/identity verb %q", c.Verb)
		}
	}
}

func TestHookCodexOldDaemonFailsClosedBeforeJoin(t *testing.T) {
	dir, err := os.MkdirTemp("", "pghold")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbSessionSupport {
			return &core.Error{Code: core.CodeInvalid, Message: "unknown verb " + proto.VerbSessionSupport}
		}
		return map[string]any{}
	})
	t.Setenv("PIGGERY_ID", "")
	t.Setenv("PIGGERY_TOKEN", "")
	t.Setenv("PIGGERY_DISABLED", "")
	old := sessionHost
	sessionHost = func(...string) string { return "codex:9:1" }
	t.Cleanup(func() { sessionHost = old })
	e := &env{dir: dir, stdout: io.Discard}
	e.runHook("codex", "SessionStart", strings.NewReader(`{"session_id":"thread-1","source":"startup","cwd":"/project"}`), io.Discard)
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) != 1 || d.calls[0].Verb != proto.VerbSessionSupport {
		t.Fatalf("old daemon calls = %v, want only session.support", d.calls)
	}
}

// A Codex TUI after a daemon restart: its participant is gone and the next
// hook rejoins by its exact session ref and sends its event again.
func TestHookCodexRejoinsWhenHostUnknown(t *testing.T) {
	dir, err := os.MkdirTemp("", "pghookr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	events := 0
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbSessionSupport {
			return proto.SessionSupportResult{ThreadAuth: true}
		}
		if verb == proto.VerbHarnessEvent {
			if events++; events == 1 {
				return &core.Error{Code: core.CodeUnauthorized, RuleID: "host.unknown", Message: "no live session with this host"}
			}
		}
		return map[string]any{}
	})
	t.Setenv("PIGGERY_ID", "")
	t.Setenv("PIGGERY_TOKEN", "")
	t.Setenv("PIGGERY_DISABLED", "")
	old := sessionHost
	sessionHost = func(...string) string { return "codex:9:1" }
	t.Cleanup(func() { sessionHost = old })
	e := &env{dir: dir, stdout: io.Discard}
	e.runHook("codex", "UserPromptSubmit", strings.NewReader(`{"session_id":"s1","turn_id":"t1","cwd":"/w"}`), io.Discard)
	var verbs []string
	for _, c := range d.calls {
		verbs = append(verbs, c.Verb)
	}
	var j core.JoinAutoArgs
	if len(d.calls) == 4 {
		json.Unmarshal(d.calls[2].Args, &j)
	}
	if strings.Join(verbs, ",") != "session.support,harness.event,join.auto,harness.event" || j.HarnessRef != "s1" || j.Host != "codex:9:1" || d.calls[3].Auth.HarnessRef != "s1" {
		t.Fatalf("calls %v, join %+v", verbs, j)
	}
}

// The pi, omp and dsh extensions' protocol version is written by hand in TypeScript; it must be the daemon's.
func TestExtensionsProtocolVersion(t *testing.T) {
	pi, err := piext.Files.ReadFile("index.ts")
	if err != nil {
		t.Fatal(err)
	}
	omp, err := ompext.Tree()
	if err != nil {
		t.Fatal(err)
	}
	dsh, err := dshext.Tree()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("const PROTOCOL_VERSION = %d;", core.ProtocolVersion)
	if !strings.Contains(string(pi), want) {
		t.Fatalf("extensions/pi/index.ts lacks %q", want)
	}
	if !strings.Contains(string(omp[ompext.Entry]), want) {
		t.Fatalf("extensions/omp/index.ts lacks %q", want)
	}
	if !strings.Contains(string(dsh[dshext.Entry]), want) {
		t.Fatalf("extensions/dsh/index.mjs lacks %q", want)
	}
}

// piggery mcp lists the built-in tools exactly as tools.json defines them (names, parameters and
// required ones), and no built-in manifest declares a tool of the same name (core keeps the two
// apart only by that convention).
func TestBuiltinToolsFromFile(t *testing.T) {
	var file struct {
		Tools []struct {
			Name       string
			Parameters struct {
				Properties map[string]any
				Required   []string
			}
		}
	}
	if err := json.Unmarshal(piext.Tools, &file); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	s := &mcpServer{host: "claude:9:1", ready: ready} // not placed yet: a solo's tools, every built-in
	b, _ := json.Marshal(s.toolList())
	var listed []struct {
		Name        string
		InputSchema struct {
			Properties map[string]any
			Required   []string
		}
	}
	json.Unmarshal(b, &listed)
	if len(listed) != len(file.Tools) {
		t.Fatalf("listed %d tools, the file has %d", len(listed), len(file.Tools))
	}
	for i, f := range file.Tools {
		l := listed[i]
		if l.Name != f.Name || !slices.Equal(slices.Sorted(maps.Keys(l.InputSchema.Properties)), slices.Sorted(maps.Keys(f.Parameters.Properties))) ||
			!slices.Equal(l.InputSchema.Required, f.Parameters.Required) {
			t.Errorf("listed %+v, file %+v", l, f)
		}
	}

	manifests, _ := filepath.Glob("../../manifests/*.yaml")
	for _, m := range manifests {
		raw, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		var man struct{ Tools map[string]any }
		if err := yaml.Unmarshal(raw, &man); err != nil {
			t.Fatal(m, err)
		}
		for _, f := range file.Tools {
			if _, ok := man.Tools[f.Name]; ok {
				t.Errorf("%s declares a tool named %q, a built-in", m, f.Name)
			}
		}
	}
	if len(manifests) == 0 {
		t.Fatal("no built-in manifests found")
	}
}

// A tool call right after tools/list must find the connection: once ready is closed (tools/list
// and every call wait on it) the identity and the connection that made it are both there. Fails
// within a few -count runs when ready closes at identify, before the connection is kept.
func TestMCPReadyMeansConnected(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		return core.IdentifyResult{ParticipantID: "P", RunID: "R", Tools: []string{"send"}}
	})
	for i := 0; i < 200; i++ {
		s := &mcpServer{dir: dir, id: "P", token: "T", run: "R", out: io.Discard, ready: make(chan struct{})}
		done := make(chan *daemonConn)
		go func() {
			c, _ := s.connect()
			done <- c
		}()
		<-s.ready
		if id, c := s.identityWithin(0); id == nil || c == nil {
			t.Fatalf("round %d: ready is closed but identity = %v, conn = %v", i, id, c)
		}
		if c := <-done; c != nil {
			c.close()
		}
	}
}

// A tool call while the daemon connection is down waits for the next one and runs on it, instead
// of failing with "the connection dropped".
func TestMCPCallWaitsThroughReconnect(t *testing.T) {
	dir, err := os.MkdirTemp("", "pgmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startFakeDaemon(t, dir, func(verb string, _ json.RawMessage) any {
		if verb == proto.VerbSend {
			return core.SendResult{Seq: 7}
		}
		return core.IdentifyResult{ParticipantID: "P", RunID: "R", Tools: []string{"send"}}
	})
	s := &mcpServer{dir: dir, id: "P", token: "T", run: "R", out: io.Discard, ready: make(chan struct{})}
	s.startKeepConnected()
	send := func() (string, error) {
		return s.callTool("send", json.RawMessage(`{"to":"lead","body":"hi"}`), "")
	}
	if txt, err := send(); err != nil || txt != "sent #7" {
		t.Fatalf("first send = %q, %v", txt, err)
	}
	d.mu.Lock()
	d.ident.Close() // the daemon goes away; keepConnected dials again after its delay
	d.mu.Unlock()
	waitFor(t, "the drop", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.conn == nil || s.conn.isDead()
	})
	if txt, err := send(); err != nil || txt != "sent #7" {
		t.Fatalf("send across the reconnect = %q, %v; want it to wait for the new connection", txt, err)
	}
}
