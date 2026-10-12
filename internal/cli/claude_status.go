package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sting8k/piggery/internal/driver/local"
)

// Claude Code fires no hook when a turn is interrupted (Esc, an Agent SDK interrupt): the turn's
// batch stays open and holds every wake. Claude does keep a file for each live session,
// <config dir>/sessions/<pid>.json, whose status is busy while a model call or a tool runs, waiting
// at a permission prompt, shell while a background command runs, and idle once the session waits for
// its user; it turns idle within 0.1 s of an Esc, with no Stop hook (captures
// testdata/fixtures/claude-2.1.296). The file is Claude's own and not documented: piggery reads it
// only to act as the hook that is missing, and does nothing when anything about it is not as seen.

// claudeIdleAfter is how long the file has to say idle before piggery believes it: a normal turn's
// Stop hook comes 10 to 110 ms before the file goes idle, and a nudge starts the next turn at once.
const claudeIdleAfter = 3 * time.Second

// claudeConfigDir is Claude's config dir (CLAUDE_CONFIG_DIR, else ~/.claude).
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// claudeSession is what piggery reads of a session file.
type claudeSession struct {
	PeerProtocol    int    `json:"peerProtocol"`
	PID             int    `json:"pid"`
	Status          string `json:"status"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"` // unix ms
}

// claudeSessionOf reads the session file of the claude process in host ("claude:<pid>:<start>"). ok is
// false for anything that is not the file of that process in the format piggery knows: no file, not
// JSON, another peerProtocol, no status or time, another pid.
func claudeSessionOf(host string) (cs claudeSession, ok bool) {
	pid, _, ok := claudeHostPID(host)
	if !ok {
		return cs, false
	}
	b, err := os.ReadFile(filepath.Join(claudeConfigDir(), "sessions", strconv.Itoa(pid)+".json"))
	if err != nil || json.Unmarshal(b, &cs) != nil {
		return cs, false
	}
	return cs, cs.PeerProtocol == 1 && cs.PID == pid && cs.Status != "" && cs.StatusUpdatedAt > 0
}

// claudeHostPID is the pid and start time of a claude host string.
func claudeHostPID(host string) (pid int, start int64, ok bool) {
	name, rest, _ := strings.Cut(host, ":")
	p, s, _ := strings.Cut(rest, ":")
	pid, err := strconv.Atoi(p)
	start, err2 := strconv.ParseInt(s, 10, 64)
	return pid, start, name == "claude" && err == nil && err2 == nil && pid > 1
}

// claudeAlive is whether the process of host is still the one the host names (a reused pid has
// another start time).
func claudeAlive(host string) bool {
	pid, start, ok := claudeHostPID(host)
	return ok && local.ProcessStartTime(pid) == start
}

// interruptWatch ends the turn of a Claude session that was interrupted. poll is called about once
// a second; the file is the session's, turnOpen asks the daemon whether this participant has a turn
// in progress, end sends it the interrupted turn_end, alive checks the process.
type interruptWatch struct {
	read     func() (claudeSession, bool)
	alive    func() bool
	turnOpen func() (bool, error)
	end      func() error
	after    time.Duration

	seen     int64     // the statusUpdatedAt of the idle status last seen
	seenTime time.Time // when it was first seen
	done     int64     // the statusUpdatedAt acted on (or found to need nothing)
}

func (w *interruptWatch) poll(now time.Time) {
	cs, ok := w.read()
	if !ok || cs.Status != "idle" {
		w.seen = 0
		return
	}
	if cs.StatusUpdatedAt != w.seen {
		w.seen, w.seenTime = cs.StatusUpdatedAt, now
	}
	// Idle for `after` by the file's clock and by ours (it may be a stale file we only just found).
	if cs.StatusUpdatedAt == w.done || now.Sub(time.UnixMilli(cs.StatusUpdatedAt)) < w.after || now.Sub(w.seenTime) < w.after {
		return
	}
	if !w.alive() {
		return
	}
	open, err := w.turnOpen()
	if err != nil {
		return // the daemon is away: look again next time
	}
	w.done = cs.StatusUpdatedAt
	if !open {
		return
	}
	// A prompt submitted while the daemon was asked opens a new turn there, and the end below would
	// close that one. Claude writes busy when it takes the prompt, before its hook reaches the daemon:
	// act only if the file still says the same idle.
	if again, ok := w.read(); !ok || again.Status != "idle" || again.StatusUpdatedAt != cs.StatusUpdatedAt {
		return
	}
	if w.end() != nil {
		w.done = 0
	}
}
