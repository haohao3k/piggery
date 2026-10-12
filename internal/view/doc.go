package view

import (
	"time"

	"github.com/sting8k/piggery/internal/core"
)

// Version is the shape of Doc (`piggery ps --view`): a reader checks it and refuses another. Bump it
// when a field changes meaning or goes; a field that is added does not change it.
const Version = 1

// Doc is everything the views decide about one snapshot, for a client that draws it
// (such as PigMonitor). Folding and tab filtering stay with the client: every team is listed open, once, with its
// default (Row.Open), the tabs that list it (Tabs) and what to do to it (Row.Actions).
type Doc struct {
	Version     int        `json:"version"`
	GeneratedAt int64      `json:"generated_at"` // unix ms: the ages are as of then
	Summary     Summary    `json:"summary"`
	Tabs        []Tab      `json:"tabs"`
	All         List       `json:"all"`    // the All tab: every open team, closed ones of the last hour, the solos
	Closed      List       `json:"closed"` // the Closed tab: every closed team
	Board       Board      `json:"board"`
	Events      []EventRow `json:"events"`
	// Details is the Overview of every id in All and Closed (a participant's, a team line's, a gone line's), by id.
	Details map[string]Detail `json:"details"`
}

// Board is what a board by status draws from: one column per status, and every directory (open
// teams, every closed team, solos) in the list's order, each with its bucket and whether it starts
// folded (Dir.Bucket, Dir.Folded). It is one list, so a client does not merge All and Closed; a
// column's title is Column.Word, the state text of its plain state ("● working", "◐ waiting"), so a
// card whose state_text differs from it (a queued one in the waiting column) is the one to say its state.
type Board struct {
	Columns []Column `json:"columns"`
	Dirs    []Dir    `json:"dirs"`
}

// Column is a status and the state text that titles its column.
type Column struct {
	Status Status `json:"status"`
	Word   string `json:"word"`
}

// Summary is the header's counts.
type Summary struct {
	Teams   int `json:"teams"`
	Working int `json:"working"`
	Idle    int `json:"idle"`
	Waiting int `json:"waiting"` // members and solos whose status is waiting: queued, starting, waiting on a permission, parked
	Held    int `json:"held"`
	Unacked int `json:"unacked"`
}

// SnapshotInput is what Snapshot reads besides the snapshot: the stats of the logs read, and which
// sessions' transcripts can be read.
type SnapshotInput struct {
	Now      time.Time
	Stats    map[string]Stats
	Readable func(*core.Transcript) bool
}

// Snapshot is the Doc of s.
func Snapshot(s core.State, in SnapshotInput) Doc {
	open := map[string]bool{}
	for _, t := range Teams(s) {
		open[t.ID] = true
	}
	list := func(tab string) List {
		return BuildList(ListInput{State: s, Tab: tab, Now: in.Now, Open: open, Gone: open, Stats: in.Stats, Readable: in.Readable})
	}
	tabsOf := map[string][]string{} // a team's tabs
	for _, t := range s.Teams {
		tabsOf[t.ID] = []string{"", t.ID}
	}
	for _, c := range s.Closed {
		tabsOf[c.ID] = []string{TabClosed}
		if Recent(c, in.Now) {
			tabsOf[c.ID] = []string{"", TabClosed}
		}
	}
	board := list(tabEvery)
	if board.Dirs == nil {
		board.Dirs = []Dir{}
	}
	d := Doc{Version: Version, GeneratedAt: in.Now.UnixMilli(), Tabs: Tabs(s, in.Now), All: list(""), Closed: list(TabClosed),
		Board:  Board{Dirs: board.Dirs},
		Events: EventRows(s, len(s.Events), in.Now), Details: map[string]Detail{}}
	d.Summary.Working, d.Summary.Idle = Activity(s)
	d.Summary.Waiting = waiting(s)
	d.Summary.Teams, d.Summary.Held, d.Summary.Unacked = len(s.Teams), s.Held, s.Unacked
	for _, st := range []Status{StatusWorking, StatusIdle, StatusWaiting, StatusGone} {
		d.Board.Columns = append(d.Board.Columns, Column{Status: st, Word: st.Text()})
	}
	for _, l := range []*List{&d.All, &d.Closed, {Dirs: d.Board.Dirs}} {
		for di := range l.Dirs {
			for bi := range l.Dirs[di].Blocks {
				b := &l.Dirs[di].Blocks[bi]
				if b.Rows == nil {
					b.Rows = []Row{}
				}
				if h := b.Head; h != nil {
					h.Tabs = tabsOf[h.ID]
					d.describe(s, TeamRow+h.ID, in)
				}
				for i := range b.Rows {
					r := &b.Rows[i]
					switch {
					case r.Kind == KindSolo:
						r.Tabs = []string{""}
					case r.Kind == KindTeam && r.Closed == "": // an open team that is all gone: one line in All only
						r.Tabs = []string{""}
					default:
						r.Tabs = tabsOf[r.Team]
					}
					if r.Kind == KindTeam || r.Kind == KindGone {
						r.Open = false // top starts it folded; the members are listed for the client to show
					}
					d.describe(s, r.ID, in)
				}
			}
		}
		if l.Dirs == nil {
			l.Dirs = []Dir{}
		}
	}
	return d
}

func (d *Doc) describe(s core.State, id string, in SnapshotInput) {
	if _, ok := d.Details[id]; !ok {
		if dt := Describe(s, id, in.Stats, in.Now); dt.Kind != DetailNone {
			d.Details[id] = dt
		}
	}
}

// waiting is how many members and solos are in a state whose status is waiting.
func waiting(s core.State) (n int) {
	for _, t := range s.Teams {
		for _, m := range t.Members {
			if StatusOf(m.State) == StatusWaiting {
				n++
			}
		}
	}
	for _, sl := range s.Solos {
		if StatusOf(sl.State) == StatusWaiting {
			n++
		}
	}
	return n
}
