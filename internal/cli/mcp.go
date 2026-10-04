package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	piext "github.com/sting8k/piggery/extensions/pi"
	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/proto"
)

// `piggery mcp` is the MCP half of the Claude Code adapter: a
// stdio MCP server Claude starts as a child. It identifies the participant of PIGGERY_ID/
// PIGGERY_TOKEN with the run PIGGERY_RUN_ID (new_run false: the daemon's process row owns the
// run), serves the role's tools (send, inbox, who, agent and the declared ones) and keeps the
// connection open for pushes: wake -> one nudge line into Claude's inbox socket (C8: only a
// child of claude is let in under bypassPermissions), role -> identify again and tell Claude
// the tool list changed. Batches and acks are the daemon's (hooks, `piggery hook claude`).
//
// With no PIGGERY_* it serves a harness session the Human opened. The exact
// session reference comes from Claude's environment or Codex's per-call
// _meta.threadId; the process host is only a lineage check. The role card goes
// out as the server's instructions. The daemon not running is normal: tools
// answer that piggery is not running, and it keeps trying.

// mcpPrefix is how Claude names this server's tools (server key "piggery" in --mcp-config).
const mcpPrefix = "mcp__piggery__"

// builtinTool is one of the built-in model tools every adapter offers, as extensions/pi/tools.json
// defines it: a name without prefix, a text where {tool:X} is X's name for the
// reader, and a JSON Schema object for the arguments.
type builtinTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// builtinTools are the file's tools in its order (the order they are listed); mcpBaseTools their
// names, the tools every role may get.
var builtinTools, mcpBaseTools = func() ([]builtinTool, []string) {
	var f struct{ Tools []builtinTool }
	if err := json.Unmarshal(piext.Tools, &f); err != nil {
		panic("extensions/pi/tools.json: " + err.Error())
	}
	var names []string
	for _, t := range f.Tools {
		names = append(names, t.Name)
	}
	return f.Tools, names
}()

type mcpServer struct {
	dir               string
	id, token, run    string
	host              string // a session the Human opened: process lineage (no id/token)
	harness           string // claude or codex: the process that started this server
	ref, sock, sockTk string // exact session ref, then CLAUDE_CODE_MESSAGING_SOCKET/_TOKEN
	out               io.Writer
	outMu             sync.Mutex

	mu             sync.Mutex
	callMu         sync.Mutex // serializes interactive calls while an exact ref is bound
	conn           *daemonConn
	ident          *core.IdentifyResult
	cardSent       bool          // Codex receives its startup role card in the first bound result
	ready          chan struct{} // closed when the first identify is done and its connection is kept
	listed         bool          // Claude has read the tool list (list_changed is worth sending)
	nudges         int
	identErr       error
	readyOnce      sync.Once
	connectStarted bool
}

func (e *env) mcp(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: mcp takes no arguments", errUsage)
	}
	s := &mcpServer{
		dir: e.dir, id: os.Getenv("PIGGERY_ID"), token: os.Getenv("PIGGERY_TOKEN"), run: os.Getenv("PIGGERY_RUN_ID"),
		ref: os.Getenv("CLAUDE_CODE_SESSION_ID"), sock: os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"),
		sockTk: os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN"), out: e.stdout, ready: make(chan struct{}),
	}
	if s.id == "" && s.token == "" {
		if os.Getenv("PIGGERY_DISABLED") == "1" {
			return s.serve(os.Stdin) // started from inside a piggery session's tool: no piggery here
		}
		s.host = sessionHost()
		if s.host == "" {
			return s.serve(os.Stdin)
		}
		s.harness, _, _ = strings.Cut(s.host, ":")
	} else if s.id == "" || s.token == "" || s.run == "" {
		return errors.New("piggery mcp: PIGGERY_ID, PIGGERY_TOKEN and PIGGERY_RUN_ID must be set (the harness runs it for a piggery worker)")
	} else if h := sessionHost(); h != "" {
		s.harness, _, _ = strings.Cut(h, ":")
	}
	s.harness = s.profile().name
	// Codex 0.160 does not put its thread id in the MCP child's environment. Its
	// first useful identity arrives in tools/call _meta.threadId, so do not start a
	// host-only connection that could select another session. Worker connections
	// and Claude's session-id environment path can start immediately.
	if s.id != "" || s.ref != "" {
		s.startKeepConnected()
	}
	return s.serve(os.Stdin)
}

func (s *mcpServer) startKeepConnected() {
	s.mu.Lock()
	if s.connectStarted {
		s.mu.Unlock()
		return
	}
	s.connectStarted = true
	s.mu.Unlock()
	go s.keepConnected()
}

// keepConnected holds one identified connection to the daemon for the life of the process,
// dialing again (no autostart: the daemon runs this worker) when it drops.
func (s *mcpServer) keepConnected() {
	delay := 200 * time.Millisecond
	for {
		c, err := s.connect()
		if err == nil {
			delay = 200 * time.Millisecond
			for p := range c.pushes {
				s.onPush(p)
			}
			// Dropped: a tool call now waits for the next connection (callTool) instead of
			// writing to this one.
			s.mu.Lock()
			if s.conn == c {
				s.conn = nil
			}
			s.mu.Unlock()
			c.close()
		} else if stale(err) && !(s.host != "" && ruleID(err) == "host.unknown") {
			s.mu.Lock()
			s.identErr = err
			s.connectStarted = false
			s.mu.Unlock()
			s.readyOnce.Do(func() { close(s.ready) })
			return // another process owns this run, or the token is gone: stop driving it
		} else if isSessionAuthUnsupported(err) {
			s.mu.Lock()
			s.identErr = err
			s.connectStarted = false
			s.mu.Unlock()
			s.readyOnce.Do(func() { close(s.ready) })
			fmt.Fprintf(os.Stderr, "piggery mcp: %v\n", err)
			return // never retry an old daemon: joining by host would reintroduce cross-project routing
		}
		time.Sleep(delay)
		delay = min(delay*2, 5*time.Second)
	}
}

func ruleID(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.RuleID
	}
	return ""
}

func stale(err error) bool {
	var ce *core.Error
	return errors.As(err, &ce) && (ce.Code == core.CodeUnauthorized || ce.RuleID == "run.stale")
}

func (s *mcpServer) connect() (*daemonConn, error) {
	s.mu.Lock()
	host, ref, harness := s.host, s.ref, s.harness
	s.mu.Unlock()
	if host != "" && ref == "" {
		return nil, errSessionIdentity
	}
	cl, err := Dial(s.dir, false)
	if err != nil {
		return nil, err
	}
	if host != "" && harness == "codex" {
		// Codex hooks are the authority that registers a thread and its cwd. The
		// MCP child has no verified per-call cwd, so it must never create or retarget
		// a participant from its inherited process directory. A session capability
		// probe also prevents an old daemon from treating the exact fields below as
		// a host-only request.
		if err := requireSessionAuth(cl); err != nil {
			cl.Close()
			return nil, err
		}
	} else if host != "" {
		wd, _ := os.Getwd()
		var jr core.JoinResult
		if _, err := cl.CallInto(proto.VerbJoinAuto, core.JoinAutoArgs{Cwd: wd, Harness: harness, Mode: "interactive",
			HarnessRef: ref, Source: "startup", Host: host}, &jr); err != nil {
			cl.Close()
			return nil, err
		}
	}
	if host != "" {
		wd, _ := os.Getwd()
		cl.AsSession(host, ref, wd)
	} else {
		cl.AsParticipant(s.id, s.token)
	}
	c := newDaemonConn(cl)
	if err := s.identify(c); err != nil {
		c.close()
		return nil, err
	}
	s.mu.Lock()
	if s.host != host || s.ref != ref {
		s.mu.Unlock()
		c.close()
		return nil, errors.New("piggery session binding changed while connecting")
	}
	s.conn = c
	s.mu.Unlock()
	// Ready only now: what waits on it (tools/list, a tool call) takes the identity and the
	// connection together, and one without the other fails the call.
	s.readyOnce.Do(func() { close(s.ready) })
	return c, nil
}

var errSessionIdentity = errors.New("piggery session identity is unavailable; provide the exact harness session reference")

// bindSession records the exact harness session supplied by the MCP caller. It
// deliberately does not derive identity from host or cwd. Codex keeps an MCP
// child across /clear, so an explicit new thread id may replace the previous
// one; the daemon's session authentication validates that transition.
func (s *mcpServer) bindSession(ref string) error {
	if ref == "" {
		return errSessionIdentity
	}
	if s.host == "" {
		return nil // PIGGERY_ID/TOKEN workers already have explicit identity.
	}
	s.mu.Lock()
	old := s.ref
	if old == ref {
		if isSessionAuthUnsupported(s.identErr) {
			err := s.identErr
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		s.startKeepConnected()
		return nil
	}
	// Claude supplies CLAUDE_CODE_SESSION_ID in its MCP environment. If it also
	// sends metadata, a mismatch is a cross-session attempt and must not switch
	// the connection. Codex has no env ref, and /clear is represented by the new
	// explicit thread id, so it may rotate this field.
	if old != "" && s.harness != "codex" {
		s.mu.Unlock()
		return fmt.Errorf("piggery session identity mismatch: bound %q, request %q", old, ref)
	}
	s.ref = ref
	c := s.conn
	s.conn = nil
	s.ident = nil
	s.identErr = nil
	s.cardSent = false
	s.mu.Unlock()
	if c != nil {
		c.close()
	}
	s.startKeepConnected()
	return nil
}

func (s *mcpServer) identify(c *daemonConn) error {
	s.mu.Lock()
	host, ref, harness, run := s.host, s.ref, s.harness, s.run
	s.mu.Unlock()
	var r core.IdentifyResult
	a := core.IdentifyArgs{RunID: run, Harness: harness, HarnessRef: ref, ToolPrefix: mcpPrefix,
		ProtocolVersion: core.ProtocolVersion}
	if host != "" {
		// The run is the exact session supplied by the harness. The host is only a
		// process-lineage check; it cannot select a participant by itself.
		a.Mode, a.Capabilities = "interactive", []string{core.CapWake, core.CapSteer}
	}
	err := c.call(proto.VerbIdentify, a, &r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.host != host || s.ref != ref {
		s.mu.Unlock()
		return errors.New("piggery session binding changed while identifying")
	}
	previousProtocol, hadIdentity := 0, s.ident != nil
	if hadIdentity {
		previousProtocol = s.ident.ProtocolVersion
	}
	changed := s.ident == nil || !slices.Equal(s.ident.Tools, r.Tools)
	notify := changed && s.listed
	s.ident = &r
	s.mu.Unlock()
	if note := protocolNote(r.ProtocolVersion, harness); note != "" && (!hadIdentity || previousProtocol != r.ProtocolVersion) {
		fmt.Fprintln(os.Stderr, "piggery mcp: "+note)
	}
	if notify {
		s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
	}
	return nil
}

func (s *mcpServer) onPush(p proto.Push) {
	switch p.Event {
	case proto.EventWake:
		s.profile().wake(s, p.Ref)
	case proto.EventRole:
		s.mu.Lock()
		c := s.conn
		s.mu.Unlock()
		if c != nil {
			_ = s.identify(c)
		}
	}
}

// nudge writes one line into Claude's inbox socket so an idle session starts a turn (C7, C8);
// the mail itself comes with that turn (hook turn_start). #N differs every time: Claude drops
// identical repeats. The priority is Claude's default ("next"): "now" would abort a running tool.
func (s *mcpServer) nudge() {
	if s.sock == "" {
		return
	}
	s.mu.Lock()
	s.nudges++
	n := s.nudges
	s.mu.Unlock()
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": fmt.Sprintf("[piggery] wake #%d: you have new mail; it is shown with this turn.", n)}})
	var b strings.Builder
	if s.sockTk != "" {
		auth, _ := json.Marshal(map[string]string{"type": "auth", "token": s.sockTk})
		b.Write(auth)
		b.WriteByte('\n')
	}
	b.Write(msg)
	b.WriteByte('\n')
	c, err := net.DialTimeout("unix", s.sock, 2*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "piggery mcp: wake: %v\n", err)
		return
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(c, b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "piggery mcp: wake: %v\n", err)
	}
}

// queueNudge wakes an idle Codex TUI: `codex queue` stores a message for the session's
// thread; the TUI picks it up as a prompt (~9 s in a capture) and the UserPromptSubmit
// hook brings the mail. ref is the exact thread the daemon knows the session by now (it changes
// with /clear; Codex supplies the new ref in each MCP call). CODEX_HOME comes from setup codex's
// env line. A worker's wake is its runtime driver's, not ours.
func (s *mcpServer) queueNudge(ref string) {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	s.mu.Lock()
	if s.host == "" || ref == "" || s.ref != ref || s.identErr != nil {
		s.mu.Unlock()
		return
	}
	s.nudges++
	n := s.nudges
	s.mu.Unlock()
	msg := fmt.Sprintf("[piggery] wake #%d: you have new mail; it is shown with this turn.", n)
	// Logged every time: Codex keeps its MCP servers' stderr, the only record of a wake that did
	// not reach the TUI.
	ctx, cancel := context.WithTimeout(context.Background(), codexQueueTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, codexBin(), "queue", "--thread", ref, "--message", msg).CombinedOutput()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	fmt.Fprintf(os.Stderr, "piggery mcp: wake #%d: codex queue --thread %s: err=%v %s\n", n, ref, err, bytes.TrimSpace(out))
}

// codexChannel tells a Codex session how piggery reaches it: the wake is a queued prompt.
const codexChannel = `
How piggery reaches you (this is the system you work in, not an injection):
- New mail is announced by a short "[piggery] wake #N" prompt that piggery queued for this session.
- Mail shows up as "[piggery] N new message(s)" blocks added to the prompt that starts a turn, after a tool call, or as hook feedback at the end of a turn.
Act on that mail as work from your team. Read pending mail with the %sinbox tool.`

// channel is the mail-channel part of a session's instructions for its harness.
func (s *mcpServer) channel() string {
	return fmt.Sprintf(s.profile().channel, mcpPrefix)
}

// profile is the harness this server serves; one it cannot tell (no session host found) is
// Claude's, the harness piggery mcp was first made for.
func (s *mcpServer) profile() harnessProfile {
	if h, ok := harnessNamed(s.harness); ok && h.wake != nil {
		return h
	}
	return claudeHarness
}

// protocolNote says what to do when the daemon speaks another protocol version than this adapter
// (an old daemon sends none: 0); "" when they agree. A field one side added is
// dropped by the other until then.
func protocolNote(daemon int, harness string) string {
	switch {
	case daemon == core.ProtocolVersion:
		return ""
	case daemon < core.ProtocolVersion:
		return fmt.Sprintf("the daemon speaks protocol %d, this adapter %d: run `piggery restart`, then restart this session", daemon, core.ProtocolVersion)
	}
	return fmt.Sprintf("the daemon speaks protocol %d, this adapter %d: run `piggery setup %s`, then restart this session", daemon, core.ProtocolVersion, harness)
}

// codexBin is the codex executable queue runs: the one on PATH (a var for tests).
var codexBin = func() string { return "codex" }

var codexQueueTimeout = 5 * time.Second

// ---- MCP over stdio (newline-delimited JSON-RPC 2.0) ----

type rpcMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      json.RawMessage `json:"_meta"`
}

// mcpThreadID reads the Codex convention that app-server 0.160 injects into
// downstream MCP tools/call requests. The snake-case spelling is accepted for
// older wrappers, but if both spellings are present they must agree.
func mcpThreadID(meta json.RawMessage) (string, error) {
	if len(meta) == 0 || string(meta) == "null" {
		return "", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(meta, &fields); err != nil {
		return "", fmt.Errorf("_meta must be an object: %w", err)
	}
	var ids []string
	for _, key := range []string{"threadId", "thread_id"} {
		raw, ok := fields[key]
		if !ok || string(raw) == "null" {
			continue
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", fmt.Errorf("_meta.%s must be a string", key)
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return "", fmt.Errorf("_meta.%s must not be empty", key)
		}
		ids = append(ids, id)
	}
	if len(ids) == 2 && ids[0] != ids[1] {
		return "", errors.New("_meta.threadId and _meta.thread_id disagree")
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

func (s *mcpServer) serve(in io.Reader) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var m rpcMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue // a notification (initialized, cancelled): nothing to answer
		}
		res, rerr := s.handle(m)
		reply := map[string]any{"jsonrpc": "2.0", "id": m.ID}
		if rerr != nil {
			reply["error"] = rerr
		} else {
			reply["result"] = res
		}
		s.write(reply)
	}
	return sc.Err()
}

func (s *mcpServer) write(v any) {
	b, _ := json.Marshal(v)
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(append(b, '\n'))
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *mcpServer) handle(m rpcMsg) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		res := map[string]any{"protocolVersion": p.ProtocolVersion,
			"capabilities": map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":   map[string]any{"name": "piggery", "version": Version}}
		if s.host != "" {
			// A session's role card and how mail reaches it (captured: enough for the model to
			// trust and act on mail). A worker has both in its system prompt already. A later
			// role change reaches the model with the next hook (the daemon adds the new card).
			card, note := "", ""
			s.mu.Lock()
			bound := s.ref != ""
			s.mu.Unlock()
			if bound {
				if id, _ := s.identityWithin(2 * time.Second); id != nil {
					card, note = id.RoleCard, protocolNote(id.ProtocolVersion, s.harness)
					s.mu.Lock()
					s.cardSent = card != ""
					s.mu.Unlock()
				}
			}
			if !bound && s.harness == "codex" {
				card = fmt.Sprintf("[piggery] this Codex MCP connection is waiting for its thread identity. Call %swho once before relying on piggery tools; Codex supplies the binding automatically. Until then, tools/list is a generic catalog and piggery cannot route mail or wakes to this chat.\n\n", mcpPrefix)
			}
			res["instructions"] = card + s.channel()
			if note != "" {
				res["instructions"] = res["instructions"].(string) + "\n\npiggery: " + note
			}
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		var p struct {
			Meta json.RawMessage `json:"_meta"`
		}
		params := m.Params
		if len(params) == 0 || string(params) == "null" {
			params = json.RawMessage(`{}`)
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{-32602, "bad params: " + err.Error()}
		}
		if ref, err := mcpThreadID(p.Meta); err != nil {
			return nil, &rpcError{-32602, err.Error()}
		} else if ref != "" {
			s.callMu.Lock()
			defer s.callMu.Unlock()
			if err := s.bindSession(ref); err != nil {
				return nil, &rpcError{-32001, err.Error()}
			}
		}
		return map[string]any{"tools": s.toolList()}, nil
	case "tools/call":
		var p mcpToolCallParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, &rpcError{-32602, "bad params: " + err.Error()}
		}
		ref, err := mcpThreadID(p.Meta)
		if err != nil {
			return nil, &rpcError{-32602, err.Error()}
		}
		text, err := s.callTool(p.Name, p.Arguments, ref)
		if err != nil {
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": errText(err)}}, "isError": true}, nil
		}
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + m.Method}
}

func errText(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		if ce.RuleID != "" {
			return fmt.Sprintf("refused (%s): %s", ce.RuleID, ce.Message)
		}
		return ce.Message
	}
	return err.Error()
}

// identity waits (bounded) for the first identify. Unbound Codex discovery does
// not call this path; tools/list uses the static catalog until tools/call binds
// an exact thread.
func (s *mcpServer) identity() (*core.IdentifyResult, *daemonConn) {
	if s.host != "" {
		return s.identityWithin(2 * time.Second)
	}
	return s.identityWithin(10 * time.Second)
}

// mcpConnectWait is how long a tool call waits for a connection to the daemon (a restart).
const mcpConnectWait = 10 * time.Second

func (s *mcpServer) identityWithin(d time.Duration) (*core.IdentifyResult, *daemonConn) {
	select {
	case <-s.ready:
	case <-time.After(d):
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listed = true; s.ident == nil {
		return nil, nil
	}
	if s.conn != nil && s.conn.isDead() {
		return s.ident, nil
	}
	return s.ident, s.conn
}

func (s *mcpServer) toolList() []any {
	s.mu.Lock()
	session, bound := s.host != "", s.ref != ""
	s.mu.Unlock()
	// Codex does not attach thread metadata to tools/list. Advertise the
	// identity-neutral base catalog before the first call; authorization happens
	// only after tools/call supplies the exact thread id.
	if session && !bound {
		s.mu.Lock()
		s.listed = true
		s.mu.Unlock()
		return baseToolList()
	}
	id, _ := s.identity()
	if id == nil && session {
		// No daemon yet: keep discovery usable, but never treat this catalog as
		// authorization. callTool still requires an exact session identity.
		id = &core.IdentifyResult{Tools: mcpBaseTools}
	}
	if id == nil {
		return nil
	}
	tools := []any{}
	for _, t := range mcpBaseTools {
		if slices.Contains(id.Tools, t) {
			tools = append(tools, baseTool(t))
		}
	}
	return tools
}

func baseToolList() []any {
	tools := make([]any, 0, len(mcpBaseTools))
	for _, t := range mcpBaseTools {
		tools = append(tools, baseTool(t))
	}
	return tools
}

// baseTool is the MCP definition of a built-in tool (the file's, with this server's tool names).
func baseTool(name string) map[string]any {
	i := slices.IndexFunc(builtinTools, func(t builtinTool) bool { return t.Name == name })
	t := builtinTools[i]
	return map[string]any{"name": t.Name, "description": core.WithToolNames(t.Description, mcpPrefix), "inputSchema": t.Parameters}
}

func (s *mcpServer) callTool(name string, raw json.RawMessage, ref string) (string, error) {
	if s.host != "" {
		s.callMu.Lock()
		defer s.callMu.Unlock()
		s.mu.Lock()
		harness := s.harness
		boundRef := s.ref
		s.mu.Unlock()
		if harness == "codex" && ref == "" {
			return "", errSessionIdentity
		}
		if ref == "" {
			ref = boundRef // Claude's explicit environment binding; Codex has none.
		}
		if err := s.bindSession(ref); err != nil {
			return "", err
		}
	}
	id, c := s.identity()
	s.mu.Lock()
	host := s.host
	identErr := s.identErr
	s.mu.Unlock()
	// No connection now: a session the Human opened asks for the daemon (the model called a piggery
	// tool, so its user wants piggery); a worker that was placed before waits for keepConnected to
	// dial again (the daemon restarted). A call already written to a connection that then dropped
	// is not here: it fails as it did, since the old daemon may have run it.
	if (id == nil || c == nil) && identErr == nil && (host != "" || id != nil) {
		if host != "" {
			if cl, err := Dial(s.dir, true); err == nil {
				cl.Close()
			}
		}
		for end := time.Now().Add(mcpConnectWait); time.Now().Before(end) && (id == nil || c == nil); {
			time.Sleep(50 * time.Millisecond)
			id, c = s.identityWithin(0)
		}
	}
	if id == nil || c == nil {
		if identErr != nil {
			return "", identErr
		}
		return "", errors.New("piggery is not reachable right now; try again")
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	allowed := slices.Contains(id.Tools, name)
	startupCard := ""
	if allowed && s.harness == "codex" && s.host != "" && id.RoleCard != "" {
		s.mu.Lock()
		if !s.cardSent {
			s.cardSent = true
			startupCard = id.RoleCard + "\n\n"
		}
		s.mu.Unlock()
	}
	withStartupCard := func(text string) string { return startupCard + text }
	switch {
	case !allowed:
		return "", fmt.Errorf("your role has no tool %q", name)
	case name == "send":
		var r core.SendResult
		if err := c.call(proto.VerbSend, raw, &r); err != nil {
			return "", err
		}
		return withStartupCard(core.SentText(r)), nil
	case name == "inbox":
		var a struct {
			View string `json:"view"`
		}
		_ = json.Unmarshal(raw, &a)
		var msgs []core.Delivered
		// No batch: while a turn is open the daemon records the delivery in its batch.
		if err := c.call(proto.VerbInbox, core.InboxArgs{View: a.View}, &msgs); err != nil {
			return "", err
		}
		switch {
		case len(msgs) == 0 && a.View != "":
			return withStartupCard("Nothing in this view."), nil
		case len(msgs) == 0:
			return withStartupCard("No new messages."), nil
		case a.View != "":
			return withStartupCard(core.RenderMail(msgs, fmt.Sprintf("view %s, %d message(s), nothing marked read", a.View, len(msgs)), mcpPrefix, time.Now())), nil
		}
		return withStartupCard(core.RenderMail(msgs, "", mcpPrefix, time.Now())), nil
	case name == "who":
		var ps []core.Presence
		if err := c.call(proto.VerbWho, nil, &ps); err != nil {
			return "", err
		}
		return withStartupCard(core.RenderWho(ps, id.ParticipantID)), nil
	case name == "agent":
		var a core.AgentArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		var r core.AgentResult
		if err := c.call(proto.VerbAgent, a, &r); err != nil {
			return "", err
		}
		if (a.Action == core.AgentFound || a.Action == core.AgentReopen || a.Action == core.AgentClose) && s.host != "" {
			// This session is now another participant, or the same one in another role: identify
			// again (auth by host follows it) for its tools and card; Claude re-reads the tools.
			if err := s.identify(c); err != nil {
				return "", err
			}
			id, _ = s.identityWithin(0)
			tools := make([]string, len(id.Tools))
			for i, t := range id.Tools {
				tools[i] = mcpPrefix + t
			}
			yours := "your tools: " + strings.Join(tools, ", ")
			if a.Action == core.AgentFound {
				return withStartupCard(fmt.Sprintf("founded team %s; you are %s (%s), its gate; %s", r.TeamName, id.Name, id.Role, yours)), nil
			}
			return withStartupCard(agentText(a, r) + "\n" + yours), nil
		}
		return withStartupCard(agentText(a, r)), nil
	}
	return "", fmt.Errorf("your role has no tool %q", name)
}

// agentText is the model-facing result of an agent action (names and #N only).
func agentText(a core.AgentArgs, r core.AgentResult) string {
	switch {
	case r.Text != "":
		return r.Text
	case a.Action == core.AgentTail:
		lines := make([]string, len(r.Records))
		for i, rec := range r.Records {
			lines[i] = string(rec)
		}
		if len(lines) == 0 {
			return "(no output)"
		}
		return strings.Join(lines, "\n")
	case r.Exit != nil:
		b, _ := json.Marshal(r.Exit)
		return "stopped (exit " + string(b) + ")"
	case a.Action == core.AgentSpawn:
		return fmt.Sprintf("spawned %s; its task is #%d (its reply comes to you as mail)", a.Name, r.TaskSeq)
	case a.Action == core.AgentResume && r.TaskSeq != 0:
		return fmt.Sprintf("resumed %s; its task is #%d (its reply comes to you as mail)", a.Target, r.TaskSeq)
	}
	return fmt.Sprintf("%s ok: %s", a.Action, a.Target)
}

// ---- a daemon connection that also carries pushes ----

// daemonConn is one identified connection: responses are matched to calls by id, pushes (no
// id) go to the pushes channel, which closes when the connection drops.
type daemonConn struct {
	cl      *Client
	mu      sync.Mutex
	pending map[string]chan proto.Response
	pushes  chan proto.Push
	dead    chan struct{}
}

func newDaemonConn(cl *Client) *daemonConn {
	c := &daemonConn{cl: cl, pending: map[string]chan proto.Response{}, pushes: make(chan proto.Push, 16), dead: make(chan struct{})}
	go c.read()
	return c
}

func (c *daemonConn) read() {
	defer func() {
		close(c.dead)
		close(c.pushes)
	}()
	for {
		b, err := c.cl.r.ReadBytes('\n')
		if err != nil {
			return
		}
		var f struct {
			ID    string `json:"id"`
			Event string `json:"event"`
		}
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		if f.ID == "" && f.Event != "" {
			var p proto.Push
			if json.Unmarshal(b, &p) == nil {
				c.pushes <- p
			}
			continue
		}
		var resp proto.Response
		if json.Unmarshal(b, &resp) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *daemonConn) close() { c.cl.Close() }

func (c *daemonConn) isDead() bool {
	select {
	case <-c.dead:
		return true
	default:
		return false
	}
}

// call sends one request and decodes its result into out (nil: ignore it).
func (c *daemonConn) call(verb string, args, out any) error {
	c.mu.Lock()
	c.cl.next++
	req := proto.Request{ID: strconv.Itoa(c.cl.next), Verb: verb, Auth: c.cl.auth}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		req.Args = raw
	}
	ch := make(chan proto.Response, 1)
	c.pending[req.ID] = ch
	line, _ := json.Marshal(req)
	_, err := c.cl.conn.Write(append(line, '\n'))
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-c.dead:
		return errors.New("piggery: the daemon connection dropped; try again")
	}
}
