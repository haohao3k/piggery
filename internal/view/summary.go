package view

import (
	"strings"
	"time"

	"github.com/sting8k/piggery/internal/core"
)

// Activity is how many members and solos are working and idle: the header's counts.
func Activity(s core.State) (working, idle int) {
	count := func(state string) {
		switch state {
		case "working":
			working++
		case "idle":
			idle++
		}
	}
	for _, t := range s.Teams {
		for _, mem := range t.Members {
			count(mem.State)
		}
	}
	for _, sl := range s.Solos {
		count(sl.State)
	}
	return working, idle
}

// Names maps the participant ids of the snapshot to names: those the daemon labels for its events
// (closed teams' too), then every member and solo.
func Names(s core.State) map[string]string {
	out := map[string]string{}
	for id, n := range s.Names {
		out[id] = n
	}
	for _, t := range Teams(s) {
		for _, p := range t.Members {
			out[p.ID] = p.Name
		}
	}
	for _, sl := range s.Solos {
		out[sl.ID] = sl.Name
	}
	return out
}

// Tone is how loud an event row is: quiet, a refusal (amber), an exit (red).
type Tone int

const (
	ToneMuted Tone = iota
	ToneWarning
	ToneDanger
)

func (t Tone) MarshalText() ([]byte, error) {
	return []byte([...]string{"muted", "warning", "danger"}[t]), nil
}

// ToneOf is the tone of an event type.
func ToneOf(typ string) Tone {
	switch typ {
	case "denied", "held":
		return ToneWarning
	case "exited", "gone":
		return ToneDanger
	}
	return ToneMuted
}

// EventRow is an event as its cells: when, who, what, and the target when it is not the actor or
// the team.
type EventRow struct {
	Time   string `json:"time"`
	Who    string `json:"who"`
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
	Tone   Tone   `json:"tone"`
}

// EventRows are the latest events of the snapshot, newest first, n of them.
func EventRows(s core.State, n int, now time.Time) []EventRow {
	names := Names(s)
	name := func(id string) string {
		if n := names[id]; n != "" || len(id) <= 6 {
			return n
		}
		return id[len(id)-6:]
	}
	var rows []EventRow
	for i := len(s.Events) - 1; i >= 0 && len(rows) < n; i-- {
		ev := s.Events[i]
		target := ""
		if ev.RefID != "" && ev.RefID != ev.Participant && ev.RefID != ev.TeamID {
			target = name(ev.RefID)
		}
		rows = append(rows, EventRow{EventTime(ev.Ts, now), name(ev.Participant), ev.Type, target, ToneOf(ev.Type)})
	}
	return rows
}

// NoticeRow is one notice top shows: when, where it is from (the team, or the gate of a solo), its
// kind (reply, settled, failed, gate_lost) and the sentence the engine wrote.
type NoticeRow struct {
	Age   string `json:"age"`
	Where string `json:"where"`
	Kind  string `json:"kind"`
	Body  string `json:"body"`
}

// NoticeRows are the notices as top shows them, in the order given (newest first).
func NoticeRows(ns []core.NotifyMail, now time.Time) []NoticeRow {
	rows := make([]NoticeRow, len(ns))
	for i, n := range ns {
		where := n.Team
		if where == "" {
			where = n.Gate
		}
		rows[i] = NoticeRow{Age: Ago(n.CreatedAt, now), Where: orDash(where), Kind: orDash(n.Kind), Body: n.Body}
	}
	return rows
}

// orDash is s, or "-" when it is empty (a column with nothing to say, as in the list).
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// VersionNotes are what the footer can end with, longest first: the daemon's build version and,
// when this binary is another build, the fix (`dev-9057651 (cli dev-99443dc: piggery restart)`, then
// the version alone). None when the daemon does not report a version.
func VersionNotes(daemon, self string) (notes []string, mismatch bool) {
	switch {
	case daemon == "":
		return nil, false
	case daemon != self:
		return []string{daemon + " (cli " + self + ": piggery restart)", daemon}, true
	}
	return []string{daemon}, false
}

// TailKind is what a tail line is.
type TailKind int

const (
	TailText    TailKind = iota // the assistant's text
	TailTool                    // a tool call: Text "▸ name", Rest its arguments
	TailResult                  // a tool's result: Text "✓ text"
	TailError                   // a tool's error: Text "✗ name  text"
	TailWarning                 // a problem
	TailRule                    // a separator (a new run)
	TailUser                    // what the user said
)

// TailLine is a tail line (tailLine's forms) read for display: its kind and its words, whole;
// the caller cuts them to its width. Tool calls come as Text and Rest, so the arguments can take
// what the name leaves.
type TailLine struct {
	Kind TailKind `json:"kind"`
	Text string   `json:"text"`
	Rest string   `json:"rest,omitempty"`
}

func (k TailKind) MarshalText() ([]byte, error) {
	return []byte([...]string{"text", "tool", "result", "error", "warning", "rule", "user"}[k]), nil
}

// ParseTail reads l, a tail line as tailLine writes it.
func ParseTail(l string) TailLine {
	switch {
	case strings.HasPrefix(l, "> "):
		name, args, _ := strings.Cut(strings.TrimPrefix(l, "> "), " ")
		return TailLine{Kind: TailTool, Text: "▸ " + name, Rest: "  " + args}
	case strings.HasPrefix(l, "< "):
		head, text, _ := strings.Cut(strings.TrimPrefix(l, "< "), ": ")
		if name, ok := strings.CutSuffix(head, " error"); ok {
			return TailLine{Kind: TailError, Text: "✗ " + name + "  " + text}
		}
		return TailLine{Kind: TailResult, Text: "✓ " + text}
	case strings.HasPrefix(l, "! "):
		return TailLine{Kind: TailWarning, Text: l}
	case strings.HasPrefix(l, "-- "):
		return TailLine{Kind: TailRule, Text: l}
	case strings.HasPrefix(l, "user: "):
		return TailLine{Kind: TailUser, Text: l}
	}
	return TailLine{Kind: TailText, Text: strings.TrimPrefix(l, "assistant: ")}
}
