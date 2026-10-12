package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// The log cache (<dir>/cache/logs.json) keeps what ps --json read of each log between calls (its
// offset, turns, context, mark, and a resumable reader's state), so a call reads only the bytes
// each log gained: session transcripts run to tens of MB and a UI such as PigMonitor calls ps --json
// every few seconds. It is the CLI's own: the daemon never reads it, and losing it only costs one
// full read.

// cachedLog is one log's logState as the cache writes it.
type cachedLog struct {
	Off    int64           `json:"off"`
	Turns  int             `json:"turns"`
	Tokens int             `json:"tokens"`
	Seen   bool            `json:"seen,omitempty"`
	Mark   string          `json:"mark"`
	State  json.RawMessage `json:"state,omitempty"`
}

func logCachePath(dir string) string { return filepath.Join(dir, "cache", "logs.json") }

// loadLogCache is the cache of dir, empty when it is missing or unreadable.
func loadLogCache(dir string) map[string]logState {
	out := map[string]logState{}
	b, err := os.ReadFile(logCachePath(dir))
	if err != nil {
		return out
	}
	var c map[string]cachedLog
	if json.Unmarshal(b, &c) != nil {
		return out
	}
	for path, l := range c {
		out[path] = logState{off: l.Off, turns: l.Turns, tokens: l.Tokens, seen: l.Seen, mark: l.Mark, state: l.State}
	}
	return out
}

// saveLogCache writes logs (what this call read: logs no longer read drop out) to dir's cache,
// through a temporary file so a concurrent reader never sees half of it. Errors are ignored: the
// next call reads the logs again.
func saveLogCache(dir string, logs map[string]logState) {
	c := make(map[string]cachedLog, len(logs))
	for path, st := range logs {
		state := st.state
		if r, ok := st.reader.(resumable); ok {
			state = r.save()
		}
		c[path] = cachedLog{Off: st.off, Turns: st.turns, Tokens: st.tokens, Seen: st.seen, Mark: st.mark, State: state}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	path := logCachePath(dir)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".logs-*.json")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		os.Remove(tmp.Name())
	}
}
