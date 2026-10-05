package local

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/jsonobj"
)

// The opencode driver (opencode 1.x, §5g): one `opencode serve` per worker, on a port and with a
// password piggery chooses, loading piggery's plugin through OPENCODE_CONFIG_CONTENT (the Human's
// config is never written). The plugin is the worker's adapter (mail, wake, steer, acks, tools, role
// card: it binds the one session this driver makes or resumes); this driver does what the plugin
// cannot or should not:
//   - the process: wait for the listening line on stdout, create or resume the session through the
//     HTTP API and keep opencode's own `ses_...` id as the worker's harness ref;
//   - Stop: abort the session first (the only thing that ends a tool's shell, which outlives serve
//     under every signal), then the runner ends the process tree; stdin EOF does not end serve;
//   - abort, models (GET /config/providers), model and variant (a prompt with noReply that the
//     model never sees; it lands at the next idle);
//   - the log of the run: standard records (runner.go) from the SSE stream, with no part deltas.
// Wire shapes: testdata/fixtures/opencode/1.18.34/serve (every claim there has a file:line in its
// README).

// OpencodeHarness is what the opencode driver's workers run.
const OpencodeHarness = "opencode"

// OpencodeToolPrefix is how opencode names piggery's tools: the plugin's tool keys are piggery_send and the rest.
const OpencodeToolPrefix = "piggery_"

// OpencodeProfile is ~/.piggery/harness/opencode.json.
type OpencodeProfile struct {
	Cmd  string   `json:"cmd"` // default "opencode"
	Args []string `json:"args"`
	Env  []string `json:"env"` // extra KEY=VALUE
	// Model ("provider/model", as GET /config/providers lists it) and Thinking (opencode's variant
	// of that model) for workers the chain gives none to.
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
	// DisabledTools are the native tools a worker must not have: the ones that reach the Human
	// (question) or start agents (task). A role keeps one with spawn.allow_tools. Every other
	// permission is allowed: an `ask` would hang a headless worker for ever.
	DisabledTools []string `json:"disabled_tools"`
	// Blacklist names MCP servers (keys of `mcp`) of the Human's opencode setup a worker turns off.
	Blacklist []string `json:"blacklist"`
	// DisableClaudeCode keeps the skills and CLAUDE.md of the Human's ~/.claude out of the worker
	// (OPENCODE_DISABLE_CLAUDE_CODE). Off by default: a worker runs with the Human's setup.
	DisableClaudeCode bool `json:"disable_claude_code"`
	// TestedVersions are the opencode versions piggery was tested with (`opencode --version`); doctor
	// warns about another.
	TestedVersions []string `json:"tested_versions"`
}

// OpencodeProfilePath is where the opencode worker profile is read from.
func OpencodeProfilePath(dir string) string { return filepath.Join(dir, "harness", "opencode.json") }

// DefaultOpencodeProfile is the default opencode.json.
var DefaultOpencodeProfile = OpencodeProfile{
	Cmd: "opencode", Args: []string{}, Env: []string{}, Model: Inherit, Thinking: Inherit,
	DisabledTools: []string{"question", "task"}, Blacklist: []string{},
	TestedVersions: []string{"1.18.34"},
}

// opencodeStartWait bounds how long Start waits for the listening line and for the session.
var opencodeStartWait = 60 * time.Second

// opencodeHTTPWait bounds one request to the serve.
const opencodeHTTPWait = 15 * time.Second

// NewOpencode returns the opencode driver over dir.
func NewOpencode(dir string, opts Options) *Driver {
	return newWith(dir, opts, &opencodeCodec{dir: dir, state: map[*worker]*opencodeRun{}})
}

type opencodeCodec struct {
	dir string

	mu    sync.Mutex
	state map[*worker]*opencodeRun
}

// opencodeLaunch is what launch decides and started needs.
type opencodeLaunch struct {
	port        int
	password    string
	cwd         string
	participant string
	resume      string // the ses_ id to continue ("" = create)
	model       string // "provider/model" or ""
	variant     string
	deny        []string
}

// opencodeRun is what the codec keeps about one live run.
type opencodeRun struct {
	ready chan struct{} // closed when the listening line was read
	once  sync.Once

	mu        sync.Mutex
	l         opencodeLaunch
	base      string
	sessionID string
	busy      bool
	model     string // "provider/model" the session runs ("" = opencode's default)
	variant   string
	pending   *opencodeSwitch // a model switch waiting for the idle
	roles     map[string]string
	seen      map[string]bool
}

// opencodeSwitch is a model and variant to give the session at its next idle.
type opencodeSwitch struct{ model, variant string }

func (*opencodeCodec) harness() string { return OpencodeHarness }

func (*opencodeCodec) toolPrefix() string { return OpencodeToolPrefix }

func (c *opencodeCodec) defaults() (string, string) {
	p, _ := c.profile()
	return p.Model, p.Thinking
}

func (c *opencodeCodec) profile() (OpencodeProfile, error) {
	var p OpencodeProfile
	b, err := os.ReadFile(OpencodeProfilePath(c.dir))
	if errors.Is(err, os.ErrNotExist) {
		return p, fmt.Errorf("no opencode worker profile at %s (run piggery setup)", OpencodeProfilePath(c.dir))
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("worker profile %s: %w", OpencodeProfilePath(c.dir), err)
	}
	if p.Cmd == "" {
		return p, fmt.Errorf("worker profile %s: cmd is required", OpencodeProfilePath(c.dir))
	}
	inheritEmpty(&p.Model, &p.Thinking)
	return p, nil
}

func (c *opencodeCodec) run(w *worker) *opencodeRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.state[w]
	if r == nil {
		r = &opencodeRun{ready: make(chan struct{}), roles: map[string]string{}, seen: map[string]bool{}}
		c.state[w] = r
	}
	return r
}

// freePort is a port nothing listens on (opencode's `--port 0` tries 4096 first: serve/01-start.txt).
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (c *opencodeCodec) launch(s core.Spec) (launch, error) {
	prof, err := c.profile()
	if err != nil {
		return launch{}, err
	}
	entry, err := EnsureOpencodeWorker(c.dir)
	if err != nil {
		return launch{}, fmt.Errorf("worker plugin: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return launch{}, err
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return launch{}, err
	}
	l := opencodeLaunch{port: port, password: hex.EncodeToString(pw), cwd: s.Cwd, participant: s.ParticipantID,
		model: firstNonEmpty(s.Model, prof.Model), variant: firstNonEmpty(s.Thinking, prof.Thinking)}
	if s.Resume {
		l.resume = s.HarnessRef
	}
	l.deny = slices.DeleteFunc(slices.Clone(prof.DisabledTools), func(t string) bool { return slices.Contains(s.AllowTools, t) })
	content, err := opencodeConfigContent(os.Getenv("OPENCODE_CONFIG_CONTENT"), entry, prof.Blacklist)
	if err != nil {
		return launch{}, err
	}
	env := []string{
		"OPENCODE_SERVER_PASSWORD=" + l.password,
		"OPENCODE_CONFIG_CONTENT=" + content,
		"OPENCODE_PERMISSION=" + opencodePermissionJSON(l.deny),
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_LSP_DOWNLOAD=1",
	}
	if prof.DisableClaudeCode {
		env = append(env, "OPENCODE_DISABLE_CLAUDE_CODE=1")
	}
	if l.resume != "" {
		env = append(env, "PIGGERY_OPENCODE_SESSION="+l.resume)
	}
	env = append(env, prof.Env...)
	args := append(slices.Clone(prof.Args), "serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1")
	return launch{cmd: prof.Cmd, args: args, env: env, model: l.model, thinking: l.variant, data: l}, nil
}

// opencodeConfigContent is OPENCODE_CONFIG_CONTENT: the plugin (after any plugin an inherited
// OPENCODE_CONFIG_CONTENT of the daemon's own environment names), sharing off, and the blacklisted MCP
// servers turned off. The Human's config file is not read here or written.
func opencodeConfigContent(inherited, entry string, blacklist []string) (string, error) {
	var cfg jsonobj.Object
	if strings.TrimSpace(inherited) != "" {
		var err error
		if cfg, err = jsonobj.Parse([]byte(inherited)); err != nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT in the environment: %w", err)
		}
	}
	var plugins []json.RawMessage
	if raw, ok := cfg.Get("plugin"); ok {
		if err := json.Unmarshal(raw, &plugins); err != nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT in the environment: plugin: %w", err)
		}
	}
	plugins = append(plugins, jsonobj.String(OpencodePluginSpec(entry)))
	cfg = cfg.Set("plugin", rawJSONArray(plugins)).Set("share", jsonobj.String("disabled")).Set("autoupdate", json.RawMessage("false"))
	if len(blacklist) > 0 {
		var mcp jsonobj.Object
		if raw, ok := cfg.Get("mcp"); ok {
			var err error
			if mcp, err = jsonobj.Parse(raw); err != nil {
				return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT in the environment: mcp: %w", err)
			}
		}
		for _, name := range blacklist {
			mcp = mcp.Set(name, json.RawMessage(`{"enabled":false}`))
		}
		cfg = cfg.Set("mcp", mcp.Bytes(0))
	}
	var out bytes.Buffer
	if err := json.Compact(&out, cfg.Bytes(0)); err != nil {
		return "", err
	}
	return out.String(), nil
}

func rawJSONArray(items []json.RawMessage) json.RawMessage {
	parts := make([][]byte, len(items))
	for i, it := range items {
		parts[i] = it
	}
	return append(append([]byte("["), bytes.Join(parts, []byte(","))...), ']')
}

// opencodeRules are the permission rules of a worker: everything allowed, each denied tool denied
// (opencode evaluates in order, the last match wins).
func opencodeRules(deny []string) []map[string]string {
	rules := []map[string]string{{"permission": "*", "pattern": "*", "action": "allow"}}
	for _, t := range deny {
		rules = append(rules, map[string]string{"permission": t, "pattern": "*", "action": "deny"})
	}
	return rules
}

// opencodePermissionJSON is the same as OPENCODE_PERMISSION (it wins over the Human's config file;
// the session carries the rules too, which are evaluated last).
func opencodePermissionJSON(deny []string) string {
	var o jsonobj.Object
	o = o.Set("*", jsonobj.String("allow"))
	for _, t := range deny {
		o = o.Set(t, jsonobj.String("deny"))
	}
	var b bytes.Buffer
	json.Compact(&b, o.Bytes(0))
	return b.String()
}

// started waits for serve's listening line, then makes the session and starts reading its events.
// A process that ends first, or is silent past opencodeStartWait, is a failed start.
func (c *opencodeCodec) started(ctx context.Context, w *worker, l launch) error {
	r := c.run(w)
	ol := l.data.(opencodeLaunch)
	r.mu.Lock()
	r.l, r.base = ol, "http://127.0.0.1:"+strconv.Itoa(ol.port)
	r.mu.Unlock()
	select {
	case <-r.ready:
	case <-w.done:
		return fmt.Errorf("opencode ended before its server was up (exit %d %s); see its .stderr next to the log", w.exit.Code, w.exit.Signal)
	case <-time.After(opencodeStartWait):
		return fmt.Errorf("opencode did not report its server up within %s", opencodeStartWait)
	}
	sctx, cancel := context.WithTimeout(ctx, opencodeStartWait)
	defer cancel()
	id, err := r.openSession(sctx, w)
	if err != nil {
		return err
	}
	w.harnessRef = id
	rctx, stop := context.WithCancel(context.Background())
	go func() {
		<-w.done
		stop()
	}()
	go r.events(rctx, w)
	return nil
}

var opencodeListening = regexp.MustCompile(`opencode server listening on http://[^:\s]+:\d+`)

// record: serve's stdout has the listening line and nothing after it; nothing of it is logged.
func (c *opencodeCodec) record(w *worker, line []byte) record {
	if opencodeListening.Match(line) {
		r := c.run(w)
		r.once.Do(func() { close(r.ready) })
	}
	return record{}
}

// beforeStop aborts the session: its running tool's shell is detached from serve and survives every
// signal to it, so the abort is what ends it. serve ignores stdin EOF.
func (c *opencodeCodec) beforeStop(w *worker) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.run(w).abort(ctx)
	return false
}

func (c *opencodeCodec) abort(w *worker) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return c.run(w).abort(ctx)
}

// --- the HTTP API ---

// do sends one request to the serve and decodes a JSON answer into out (nil: any 2xx body is dropped).
func (r *opencodeRun) do(ctx context.Context, method, path string, body, out any) error {
	r.mu.Lock()
	l, base := r.l, r.base
	r.mu.Unlock()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, opencodeHTTPWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", l.password)
	req.Header.Set("x-opencode-directory", l.cwd)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("opencode %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(clipText(string(b), 300)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// opencodeModel is one model GET /config/providers offers.
type opencodeModel struct {
	Ref      string // provider/model, what set_model takes
	Variants []string
}

// models reads GET /config/providers, in opencode's order (jsonobj keeps the members' order).
func (r *opencodeRun) models(ctx context.Context) ([]opencodeModel, error) {
	var raw json.RawMessage
	if err := r.do(ctx, "GET", "/config/providers", nil, &raw); err != nil {
		return nil, err
	}
	top, err := jsonobj.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("opencode /config/providers: %w", err)
	}
	pr, _ := top.Get("providers")
	var providers []json.RawMessage
	if err := json.Unmarshal(pr, &providers); err != nil {
		return nil, fmt.Errorf("opencode /config/providers: %w", err)
	}
	var out []opencodeModel
	for _, p := range providers {
		po, err := jsonobj.Parse(p)
		if err != nil {
			continue
		}
		var id string
		if raw, ok := po.Get("id"); ok {
			json.Unmarshal(raw, &id)
		}
		mraw, _ := po.Get("models")
		ms, err := jsonobj.Parse(mraw)
		if err != nil {
			continue
		}
		for _, m := range ms {
			mo, _ := jsonobj.Parse(m.Val)
			var variants []string
			if v, ok := mo.Get("variants"); ok {
				if vo, err := jsonobj.Parse(v); err == nil {
					for _, x := range vo {
						variants = append(variants, x.Key)
					}
				}
			}
			out = append(out, opencodeModel{Ref: id + "/" + m.Key, Variants: variants})
		}
	}
	return out, nil
}

// pickOpencodeModel finds model in the list; the error says what is wrong.
func pickOpencodeModel(list []opencodeModel, model string) (opencodeModel, error) {
	if provider, id, ok := strings.Cut(model, "/"); !ok || provider == "" || id == "" {
		return opencodeModel{}, fmt.Errorf("model %q: want provider/model", model)
	}
	for _, m := range list {
		if m.Ref == model {
			return m, nil
		}
	}
	return opencodeModel{}, fmt.Errorf("opencode does not offer model %q (the model picker in `piggery top` lists them)", model)
}

func checkVariant(m opencodeModel, variant string) error {
	if variant == "" || slices.Contains(m.Variants, variant) {
		return nil
	}
	if len(m.Variants) == 0 {
		return fmt.Errorf("model %s has no variants, so %q is not a thinking level it runs", m.Ref, variant)
	}
	return fmt.Errorf("model %s does not run %q (variants: %s)", m.Ref, variant, strings.Join(m.Variants, ", "))
}

// openSession creates the worker's session (title, the participant in its metadata for the plugin to
// bind, everything-allowed permission rules, the model) or checks the one to resume exists.
func (r *opencodeRun) openSession(ctx context.Context, w *worker) (string, error) {
	r.mu.Lock()
	l := r.l
	r.mu.Unlock()
	if err := r.do(ctx, "GET", "/global/health", nil, nil); err != nil {
		return "", fmt.Errorf("opencode server is not answering: %w", err)
	}
	var list []opencodeModel
	if l.model != "" || l.variant != "" {
		var err error
		if list, err = r.models(ctx); err != nil {
			return "", err
		}
	}
	model := l.model
	if model == "" && l.variant != "" {
		return "", errors.New("opencode: a thinking level (variant) needs a model: set model in the profile or the role")
	}
	var picked opencodeModel
	if model != "" {
		var err error
		if picked, err = pickOpencodeModel(list, model); err != nil {
			return "", err
		}
		if err := checkVariant(picked, l.variant); err != nil {
			return "", err
		}
	}
	if l.resume != "" {
		var sess struct {
			Model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
		}
		if err := r.do(ctx, "GET", "/session/"+l.resume, nil, &sess); err != nil {
			return "", fmt.Errorf("opencode cannot resume session %s: %w", l.resume, err)
		}
		cur, variant := "", ""
		if sess.Model.ID != "" {
			cur, variant = sess.Model.ProviderID+"/"+sess.Model.ID, sess.Model.Variant
			if variant == "default" {
				variant = ""
			}
		}
		r.setSession(l.resume, cur, variant)
		if model != "" && (cur != model || variant != l.variant) { // the profile or role names another model than the session ran
			if err := r.sendSwitch(ctx, opencodeSwitch{model, l.variant}); err != nil {
				return "", err
			}
		}
		return l.resume, nil
	}
	body := map[string]any{"title": "piggery " + l.participant,
		"metadata":   map[string]string{"piggery_participant": l.participant},
		"permission": opencodeRules(l.deny)}
	if model != "" {
		provider, id, _ := strings.Cut(model, "/")
		m := map[string]string{"id": id, "providerID": provider}
		if l.variant != "" {
			m["variant"] = l.variant
		}
		body["model"] = m
	}
	var sess struct {
		ID string `json:"id"`
	}
	if err := r.do(ctx, "POST", "/session", body, &sess); err != nil || sess.ID == "" {
		return "", fmt.Errorf("opencode could not create the session: %v", firstErr(err, errors.New("no id in the answer")))
	}
	r.setSession(sess.ID, model, l.variant)
	return sess.ID, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (r *opencodeRun) setSession(id, model, variant string) {
	r.mu.Lock()
	r.sessionID, r.model, r.variant = id, model, variant
	r.mu.Unlock()
}

func (r *opencodeRun) session() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

func (r *opencodeRun) abort(ctx context.Context) error {
	id := r.session()
	if id == "" {
		return errors.New("opencode: no session yet")
	}
	return r.do(ctx, "POST", "/session/"+id+"/abort", nil, nil)
}

// sendSwitch gives the session model and variant: a prompt with noReply (no turn, no model call)
// whose only part is synthetic and ignored, so the model never sees it. The session keeps the model
// (a prompt without one runs on it); the variant is the plugin's to pass on each prompt, read from
// the session.
func (r *opencodeRun) sendSwitch(ctx context.Context, sw opencodeSwitch) error {
	provider, id, _ := strings.Cut(sw.model, "/")
	body := map[string]any{"noReply": true, "model": map[string]string{"providerID": provider, "modelID": id},
		"parts": []map[string]any{{"type": "text", "text": "piggery: model switch", "synthetic": true, "ignored": true}}}
	if sw.variant != "" {
		body["variant"] = sw.variant
	}
	if err := r.do(ctx, "POST", "/session/"+r.session()+"/prompt_async", body, nil); err != nil {
		return err
	}
	r.mu.Lock()
	r.model, r.variant = sw.model, sw.variant
	r.mu.Unlock()
	return nil
}

// switchTo validates and applies a model and variant now when the session is idle, else at its next idle.
func (r *opencodeRun) switchTo(ctx context.Context, sw opencodeSwitch) error {
	r.mu.Lock()
	if r.busy {
		r.pending = &sw
		r.mu.Unlock()
		return nil
	}
	r.pending = nil
	r.mu.Unlock()
	return r.sendSwitch(ctx, sw)
}

func (c *opencodeCodec) setModel(ctx context.Context, w *worker, model string) error {
	r := c.run(w)
	list, err := r.models(ctx)
	if err != nil {
		return err
	}
	if _, err := pickOpencodeModel(list, model); err != nil {
		return err
	}
	return r.switchTo(ctx, opencodeSwitch{model: model}) // a variant belongs to its model: it is cleared
}

func (c *opencodeCodec) setThinking(ctx context.Context, w *worker, level string) error {
	r := c.run(w)
	list, err := r.models(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	model := r.model
	r.mu.Unlock()
	if model == "" {
		if model, err = r.defaultModel(ctx); err != nil {
			return err
		}
	}
	m, err := pickOpencodeModel(list, model)
	if err != nil {
		return err
	}
	if level == "default" {
		level = ""
	}
	if err := checkVariant(m, level); err != nil {
		return err
	}
	return r.switchTo(ctx, opencodeSwitch{model: model, variant: level})
}

// defaultModel is the model opencode runs a session on when none was chosen (`model` of its config).
func (r *opencodeRun) defaultModel(ctx context.Context) (string, error) {
	var cfg struct {
		Model string `json:"model"`
	}
	if err := r.do(ctx, "GET", "/config", nil, &cfg); err != nil || cfg.Model == "" {
		return "", errors.New("opencode has no default model: set a model first")
	}
	return cfg.Model, nil
}

func (c *opencodeCodec) models(ctx context.Context, w *worker) ([]string, error) {
	list, err := c.run(w).models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.Ref
	}
	return out, nil
}

// --- the event stream ---

// events reads GET /event for the life of the run, again after a break, and logs what it means.
func (r *opencodeRun) events(ctx context.Context, w *worker) {
	for ctx.Err() == nil {
		r.readEvents(ctx, w)
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (r *opencodeRun) readEvents(ctx context.Context, w *worker) {
	r.mu.Lock()
	l, base := r.l, r.base
	r.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/event", nil)
	if err != nil {
		return
	}
	req.SetBasicAuth("opencode", l.password)
	req.Header.Set("x-opencode-directory", l.cwd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	br := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if data, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r\n"), []byte("data:")); ok {
			w.appendLog(r.standard(bytes.TrimSpace(data))...)
			r.applyPending(ctx)
		}
		if err != nil {
			return
		}
	}
}

// applyPending gives the session the model a busy-time switch asked for, once it is idle.
func (r *opencodeRun) applyPending(ctx context.Context) {
	r.mu.Lock()
	sw := r.pending
	if sw == nil || r.busy {
		r.mu.Unlock()
		return
	}
	r.pending = nil
	r.mu.Unlock()
	go func() {
		c, cancel := context.WithTimeout(ctx, commandTimeout)
		defer cancel()
		if err := r.sendSwitch(c, *sw); err != nil {
			slog.Warn("opencode model switch at idle failed", "model", sw.model, "err", err)
		}
	}()
}

// standard turns one SSE event of the worker's session into standard records (runner.go): what
// tail and top read. Text and tool parts are logged when they end, usage at each step, a turn when
// the session goes idle; part deltas and the events of instance start are dropped
// (serve/10-volume.txt). Events of another session are ignored.
func (r *opencodeRun) standard(data []byte) [][]byte {
	var ev struct {
		Type       string `json:"type"`
		Properties struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
			Info struct {
				ID, Role, SessionID string
			} `json:"info"`
			Part struct {
				ID        string `json:"id"`
				MessageID string `json:"messageID"`
				SessionID string `json:"sessionID"`
				Type      string `json:"type"`
				Text      string `json:"text"`
				Tool      string `json:"tool"`
				Ignored   bool   `json:"ignored"`
				Synthetic bool   `json:"synthetic"`
				Time      struct {
					End int64 `json:"end"`
				} `json:"time"`
				State struct {
					Status string          `json:"status"`
					Input  json.RawMessage `json:"input"`
					Output string          `json:"output"`
					Error  string          `json:"error"`
				} `json:"state"`
				Tokens struct {
					Total     int `json:"total"`
					Input     int `json:"input"`
					Output    int `json:"output"`
					Reasoning int `json:"reasoning"`
					Cache     struct {
						Read  int `json:"read"`
						Write int `json:"write"`
					} `json:"cache"`
				} `json:"tokens"`
			} `json:"part"`
			Error struct {
				Name string `json:"name"`
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
			Permission string   `json:"permission"`
			Patterns   []string `json:"patterns"`
		} `json:"properties"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return nil
	}
	p := ev.Properties
	sid := firstNonEmpty(p.SessionID, p.Info.SessionID, p.Part.SessionID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if sid != "" && sid != r.sessionID {
		return nil
	}
	var out [][]byte
	add := func(v any) { out = append(out, jsonLine(v)) }
	once := func(key string) bool {
		if r.seen[key] {
			return false
		}
		r.seen[key] = true
		return true
	}
	switch ev.Type {
	case "message.updated":
		r.roles[p.Info.ID] = p.Info.Role
	case "message.part.updated":
		pt := p.Part
		switch pt.Type {
		case "text":
			role := r.roles[pt.MessageID]
			switch {
			case role == "user" && !pt.Ignored && !pt.Synthetic && pt.Text != "":
				if once("text:" + pt.ID) {
					add(textRecord("user", pt.Text))
				}
			case role != "user" && pt.Time.End > 0 && pt.Text != "":
				if once("text:" + pt.ID) {
					add(textRecord("assistant", pt.Text))
				}
			}
		case "tool":
			switch pt.State.Status {
			case "running":
				if once("start:" + pt.ID) {
					add(map[string]any{"type": "tool_execution_start", "toolName": pt.Tool, "args": json.RawMessage(firstNonEmpty(string(pt.State.Input), "{}"))})
				}
			case "completed", "error":
				if once("end:" + pt.ID) {
					text, isErr := pt.State.Output, pt.State.Status == "error"
					if isErr {
						text = pt.State.Error
					}
					add(map[string]any{"type": "tool_execution_end", "toolName": pt.Tool, "isError": isErr,
						"result": map[string]any{"content": []map[string]string{{"type": "text", "text": clipText(text, 4000)}}}})
				}
			}
		case "step-finish":
			if once("step:" + pt.ID) {
				t := pt.Tokens
				add(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{},
					"usage": map[string]int{"input": t.Input, "output": t.Output, "cacheRead": t.Cache.Read, "cacheWrite": t.Cache.Write, "totalTokens": t.Total}}})
				add(map[string]string{"type": "turn_end"})
			}
		}
	case "session.status":
		switch p.Status.Type {
		case "busy":
			r.busy = true
		case "idle":
			if r.busy {
				r.busy = false
				add(map[string]string{"type": "agent_end"})
			}
		}
	case "session.error":
		msg := p.Error.Data.Message
		if strings.Contains(msg, "\n    at ") { // the second event of an error carries a stack trace
			break
		}
		if p.Error.Name == "MessageAbortedError" {
			msg = "aborted"
		}
		out = append(out, stdError(firstNonEmpty(msg, p.Error.Name)))
	case "permission.asked":
		out = append(out, stdError("waiting for a permission: "+p.Permission+" "+strings.Join(p.Patterns, " ")))
	}
	return out
}

// jsonLine is v as one JSON line without the HTML escaping encoding/json adds (the plugin's JSON.stringify has none).
func jsonLine(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func textRecord(role, text string) map[string]any {
	return map[string]any{"type": "message_end", "message": map[string]any{"role": role,
		"content": []map[string]string{{"type": "text", "text": clipText(text, 20000)}}}}
}

// OpencodeVersion runs `<cmd> --version`: opencode prints the bare version (1.18.34).
func OpencodeVersion(ctx context.Context, cmd string) (string, error) {
	return HarnessVersion(ctx, cmd)
}
