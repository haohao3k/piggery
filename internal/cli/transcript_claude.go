package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code's transcript (~/.claude/projects/<cwd>/<session>.jsonl, the SessionStart hook's
// transcript_path): one line per content block, so one model reply is several assistant lines
// sharing message.id and its usage (capture testdata/fixtures/claude-2.1.283/transcript.jsonl).
// A turn is one reply (one model call, like a worker's turn_end); isSidechain lines are a
// subagent's, isMeta user lines are text Claude added (hook feedback, notes), and a <synthetic>
// reply is Claude's own, no model call.

func init() {
	formats["claude"] = func() transcriptReader { return &claudeReader{seen: map[string]bool{}, tools: map[string]string{}} }
}

type claudeReader struct {
	seen  map[string]bool   // message ids counted as a turn
	last  string            // the latest of them
	tools map[string]string // tool name by tool_use id, for the result line
}

// save is the state turns need to read on (logcache.go): the latest reply's id, since its lines
// may go on past the offset. The lines of one reply are consecutive (in every transcript
// checked), so older ids never come back; tool names only name tail lines.
func (c *claudeReader) save() json.RawMessage {
	b, _ := json.Marshal(c.last)
	return b
}

func (c *claudeReader) load(state json.RawMessage) {
	if json.Unmarshal(state, &c.last) == nil && c.last != "" {
		c.seen[c.last] = true
	}
}

func (c *claudeReader) read(rec []byte) record {
	var r struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		IsMeta      bool   `json:"isMeta"`
		Message     struct {
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   *struct {
				Input       int `json:"input_tokens"`
				CacheRead   int `json:"cache_read_input_tokens"`
				CacheCreate int `json:"cache_creation_input_tokens"`
				Output      int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	var out record
	if json.Unmarshal(rec, &r) != nil || r.IsSidechain {
		return out
	}
	m := r.Message
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	json.Unmarshal(m.Content, &blocks) // a string content leaves none
	switch r.Type {
	case "assistant":
		if m.Model == "<synthetic>" { // Claude's own (e.g. an API error), not a model call
			if t := text(m.Content); t != "" {
				out.line = "! " + clip(t)
			}
			return out
		}
		if m.Usage != nil {
			u := m.Usage
			out.ctx, out.hasCtx = u.Input+u.CacheRead+u.CacheCreate+u.Output, true
		}
		if m.ID != "" && !c.seen[m.ID] {
			c.seen[m.ID], c.last, out.turnEnd = true, m.ID, true
		}
		for _, b := range blocks {
			switch {
			case b.Type == "text" && b.Text != "":
				out.line = "assistant: " + clip(b.Text)
			case b.Type == "tool_use":
				c.tools[b.ID] = b.Name
				out.line = "> " + b.Name + " " + clip(toolArgs(strings.ToLower(b.Name), b.Input)) // Bash: its command
			default:
				continue
			}
			return out
		}
	case "user":
		if r.IsMeta {
			return out
		}
		if t := text(m.Content); len(blocks) == 0 || t != "" {
			if t != "" {
				out.line = "user: " + clip(t)
			}
			return out
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				status := "ok"
				if b.IsError {
					status = "error"
				}
				out.line = "< " + prefixed(c.tools[b.ToolUseID], status) + ": " + clip(text(b.Content))
				return out
			}
		}
	}
	return out
}

// prefixed is "name status", or status alone when the name is unknown (a reader started after
// the call).
func prefixed(name, status string) string {
	if name == "" {
		return status
	}
	return name + " " + status
}

// claudeForkParent is the session id a Claude session was forked from ("" for none, or when its
// transcript cannot be read): every entry a fork copies carries forkedFrom.sessionId, the first one
// included (the SDK's forkSession, /branch). path is the transcript when the caller has it (the
// SessionStart hook); without it (the MCP server) the file is found by the session id in any project
// directory of the Claude config dir.
func claudeForkParent(session, path string) string {
	if path == "" {
		if session == "" || strings.ContainsAny(session, `/\`) {
			return ""
		}
		found, _ := filepath.Glob(filepath.Join(claudeConfigDir(), "projects", "*", session+".jsonl"))
		if len(found) == 0 {
			return ""
		}
		path = found[0]
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	line, _ := bufio.NewReaderSize(f, 1<<16).ReadBytes('\n') // the first entry only; an over-long one just fails to parse
	var first struct {
		ForkedFrom struct {
			SessionID string `json:"sessionId"`
		} `json:"forkedFrom"`
	}
	if json.Unmarshal(line, &first) != nil {
		return ""
	}
	return first.ForkedFrom.SessionID
}
