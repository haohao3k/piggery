package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sting8k/piggery/internal/core"
)

// The dsh (DeepSeek Harness 0.2) codec: `dsh --profile sdk` with piggery's plugin (extensions/dsh).
// The plugin is the worker's adapter (mail, acks, tools, role card, like pi's extension) and also
// creates the one agent under the session id piggery chose (or resumes it), so this driver starts a
// process and talks to the plugin over the stdio the sdk profile owns:
//   - stdout is JSON-RPC frames. The plugin's are `piggery/record` (a standard record for tail and
//     top: the log holds these and nothing else, not dsh's own session.event stream), `piggery/ready`
//     (the agent is up) and `piggery/result` (the answer to a command, by id).
//   - stdin closing ends dsh (the sdk profile exits at EOF, which is how Stop works); commands are
//     `piggery/abort` and `piggery/set_model {id, model?, thinking?}` notifications, which the sdk's
//     own reader drops as unknown.
// dsh's session files (zstd) are neither read nor changed. Wire shapes: testdata/fixtures/dsh/0.2.0-rc.1
// (worker-stdout.jsonl is a real run's stdout).

// DshHarness is what the dsh driver's workers run.
const DshHarness = "dsh"

// DshToolPrefix is how dsh names piggery's tools: the plugin registers piggery_send and the rest.
const DshToolPrefix = "piggery_"

// DshProfile is ~/.piggery/harness/dsh.json.
type DshProfile struct {
	Cmd string `json:"cmd"` // default "dsh"; `npx` with args ["-y", "@deepseek-ai/dsh@0.2.0-rc.1"] also works
	// Args come before `--profile sdk`.
	Args []string `json:"args"`
	Env  []string `json:"env"` // extra KEY=VALUE
	// Model ("provider/model" as dsh names routes) and Thinking (dsh's reasoning effort) for workers
	// the chain gives none to.
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
	// DisabledTools are the native tools a worker must not have: the ones that reach the Human,
	// work around piggery's mail and workers, or schedule turns of their own. A role keeps one with
	// spawn.allow_tools.
	DisabledTools []string `json:"disabled_tools"`
	// Blacklist names plugin rows (ids in `dsh --dump-config`) of the Human's dsh setup a worker
	// turns off, e.g. an MCP server row.
	Blacklist []string `json:"blacklist"`
	// TestedVersions are the dsh versions piggery was tested with (`dsh --version`); doctor warns
	// about another.
	TestedVersions []string `json:"tested_versions"`
}

// DshProfilePath is where the dsh worker profile is read from.
func DshProfilePath(dir string) string { return filepath.Join(dir, "harness", "dsh.json") }

// DefaultDshProfile is the default dsh.json: dsh on PATH and the native tools cut down like a
// Claude worker's (no sub-orchestration outside piggery, no plan mode or goals).
var DefaultDshProfile = DshProfile{
	Cmd: "dsh", Args: []string{}, Env: []string{}, Model: Inherit, Thinking: Inherit,
	DisabledTools: []string{"subagent", "subagent_fork", "send_message", "list_agents", "interrupt_agent", "workflow",
		"exit_plan_mode", "create_goal", "get_goal", "update_goal"},
	Blacklist:      []string{},
	TestedVersions: []string{"0.2.0-rc.1", "0.2.0-rc.2"},
}

var dshVersionRe = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?`)

// DshVersion runs `<cmd> [args] --version` and returns the version in its output (0.2.0-rc.1).
func DshVersion(ctx context.Context, cmd string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(cctx, cmd, append(args, "--version")...).Output()
	if err != nil {
		return "", err
	}
	if v := dshVersionRe.FindString(string(raw)); v != "" {
		return v, nil
	}
	return "", errors.New("no version in its --version output")
}

// dshStartWait bounds how long Start waits for the plugin's ready (dsh loads about 200 plugins;
// npx may install first).
var dshStartWait = 120 * time.Second

// NewDsh returns the dsh driver over dir.
func NewDsh(dir string, opts Options) *Driver {
	return newWith(dir, opts, &dshCodec{dir: dir, state: map[*worker]*dshRun{}})
}

type dshCodec struct {
	dir string

	mu    sync.Mutex
	state map[*worker]*dshRun
}

// dshRun is what the codec keeps about one live run.
type dshRun struct {
	ready   chan struct{} // closed by piggery/ready
	once    sync.Once
	mu      sync.Mutex
	lastErr string // the latest error the plugin logged: why the run did not start
}

func (*dshCodec) harness() string { return DshHarness }

func (*dshCodec) toolPrefix() string { return DshToolPrefix }

func (c *dshCodec) defaults() (string, string) {
	p, _ := c.profile()
	return p.Model, p.Thinking
}

func (c *dshCodec) profile() (DshProfile, error) {
	var p DshProfile
	b, err := os.ReadFile(DshProfilePath(c.dir))
	if errors.Is(err, os.ErrNotExist) {
		return p, fmt.Errorf("no dsh worker profile at %s (run piggery setup)", DshProfilePath(c.dir))
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("worker profile %s: %w", DshProfilePath(c.dir), err)
	}
	if p.Cmd == "" {
		return p, fmt.Errorf("worker profile %s: cmd is required", DshProfilePath(c.dir))
	}
	inheritEmpty(&p.Model, &p.Thinking)
	return p, nil
}

func (c *dshCodec) run(w *worker) *dshRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.state[w]
	if r == nil {
		r = &dshRun{ready: make(chan struct{})}
		c.state[w] = r
	}
	return r
}

func (c *dshCodec) launch(s core.Spec) (launch, error) {
	prof, err := c.profile()
	if err != nil {
		return launch{}, err
	}
	patch, err := EnsureDshWorker(c.dir, prof.Blacklist)
	if err != nil {
		return launch{}, fmt.Errorf("worker plugin: %w", err)
	}
	model := firstNonEmpty(s.Model, prof.Model)
	thinking := firstNonEmpty(s.Thinking, prof.Thinking) // dsh's own levels; the plugin refuses one the model does not run
	deny := slices.DeleteFunc(slices.Clone(prof.DisabledTools), func(t string) bool { return slices.Contains(s.AllowTools, t) })
	env := append([]string{
		"PIGGERY_DSH_SESSION=" + s.HarnessRef,
		"PIGGERY_DSH_MODEL=" + model,
		"PIGGERY_DSH_THINKING=" + thinking,
		"PIGGERY_DSH_DENY=" + strings.Join(deny, ","),
	}, prof.Env...)
	if s.Resume {
		env = append(env, "PIGGERY_DSH_RESUME=1")
	}
	args := append(slices.Clone(prof.Args), "--profile", "sdk", "--patch", patch)
	return launch{cmd: prof.Cmd, args: args, env: env, model: model, thinking: thinking}, nil
}

// started waits for the plugin's ready: its agent exists under the worker's session id, with the
// model and level checked. A process that ends first, or is silent past dshStartWait, is a failed start
// with the plugin's own reason.
func (c *dshCodec) started(_ context.Context, w *worker, _ launch) error {
	r := c.run(w)
	select {
	case <-r.ready:
		return nil
	case <-w.done:
		// The plugin's last error line may still be on its way from the stdout reader.
		for i := 0; i < 50 && r.err() == ""; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		return fmt.Errorf("dsh ended before its worker was up (exit %d %s): %s", w.exit.Code, w.exit.Signal, firstNonEmpty(r.err(), "no reason logged; see its .stderr next to the log"))
	case <-time.After(dshStartWait):
		return fmt.Errorf("dsh did not report its worker up within %s: %s", dshStartWait, firstNonEmpty(r.err(), "no reason logged"))
	}
}

func (r *dshRun) err() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

func (c *dshCodec) record(w *worker, line []byte) record {
	var f struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &f) != nil {
		return record{}
	}
	switch f.Method {
	case "piggery/record":
		var m struct {
			Message struct {
				ErrorMessage string `json:"errorMessage"`
			} `json:"message"`
		}
		if json.Unmarshal(f.Params, &m) == nil && m.Message.ErrorMessage != "" {
			r := c.run(w)
			r.mu.Lock()
			r.lastErr = m.Message.ErrorMessage
			r.mu.Unlock()
		}
		return record{std: [][]byte{f.Params}}
	case "piggery/ready":
		c.run(w).once.Do(func() { close(c.run(w).ready) })
	case "piggery/result":
		var m struct {
			ID string `json:"id"`
		}
		json.Unmarshal(f.Params, &m)
		return record{answers: m.ID}
	}
	return record{} // dsh's own frames (session.event, session.status): the log has none of them
}

func (*dshCodec) abort(w *worker) error {
	return w.send(map[string]any{"jsonrpc": "2.0", "method": "piggery/abort"})
}

func (c *dshCodec) setModel(ctx context.Context, w *worker, model string) error {
	if provider, id, ok := strings.Cut(model, "/"); !ok || provider == "" || id == "" {
		return fmt.Errorf("model %q: want provider/model", model)
	}
	return c.command(ctx, w, "set_model", map[string]any{"model": model})
}

// models asks the plugin (piggery/models) for what dsh's llm service lists, as provider/model.
func (c *dshCodec) models(ctx context.Context, w *worker) ([]string, error) {
	id := nextRequestID()
	line, err := w.request(ctx, commandTimeout, id, "models", map[string]any{"jsonrpc": "2.0", "method": "piggery/models", "params": map[string]any{"id": id}})
	if err != nil {
		return nil, err
	}
	var r struct {
		Params struct {
			OK     bool     `json:"ok"`
			Error  string   `json:"error"`
			Models []string `json:"models"`
		} `json:"params"`
	}
	json.Unmarshal(line, &r)
	if !r.Params.OK {
		return nil, fmt.Errorf("dsh refused models: %s", r.Params.Error)
	}
	return append([]string{}, r.Params.Models...), nil
}

func (c *dshCodec) setThinking(ctx context.Context, w *worker, level string) error {
	return c.command(ctx, w, "thinking", map[string]any{"thinking": level})
}

// command sends piggery/set_model with params and waits for its piggery/result; a refusal is an
// error carrying dsh's own words.
func (c *dshCodec) command(ctx context.Context, w *worker, name string, params map[string]any) error {
	id := nextRequestID()
	params["id"] = id
	line, err := w.request(ctx, commandTimeout, id, name, map[string]any{"jsonrpc": "2.0", "method": "piggery/set_model", "params": params})
	if err != nil {
		return err
	}
	var r struct {
		Params struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"params"`
	}
	json.Unmarshal(line, &r)
	if !r.Params.OK {
		return fmt.Errorf("dsh refused %s: %s", name, r.Params.Error)
	}
	return nil
}
