package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Operator snapshot: what `ps` and `top` show. Read-only: state tables only, no event, no state
// change.

type StateArgs struct {
	Events int `json:"events,omitempty"` // how many of the latest events to include (default 10)
}

type State struct {
	Teams []TeamState `json:"teams"`
	// Closed are the closed teams gc has not removed, newest first.
	Closed  []ClosedTeam `json:"closed"`
	Solos   []SoloState  `json:"solos"`
	Held    int          `json:"held"`    // messages held (not delivered until released)
	Unacked int          `json:"unacked"` // messages to participants not yet acked, held excluded
	Events  []Event      `json:"events"`  // the latest events, oldest first
	// Names labels every id in Events (actor or ref): a participant by its name, closed teams too;
	// a message by its #seq. A team ref has none.
	Names map[string]string `json:"names"`
	// ProtocolVersion is the daemon's; a session whose adapter's differs is out of step.
	ProtocolVersion int `json:"protocol_version"`
}

// TeamState is one open team. Held and Unacked count mail to its members (by recipient).
type TeamState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Template string `json:"template"` // the template it was founded from
	// Parent and ParentID: the participant (name, id) that called this team up as a taskforce
	// (spawn template=); "" for an ordinary team.
	Parent   string `json:"parent,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	Root     string `json:"root"`
	Gate     string `json:"gate"` // gate's name, "" when the team has none
	// GateClosed: the team's gate is closed to other teams and solos.
	GateClosed bool          `json:"gate_closed,omitempty"`
	Held       int           `json:"held"`
	Unacked    int           `json:"unacked"`
	Members    []MemberState `json:"members"`
	CreatedAt  int64         `json:"created_at"` // when it was brought up
}

// ClosedTeam is a closed team as it was left: its members with their final state.
type ClosedTeam struct {
	TeamState
	ClosedAt int64  `json:"closed_at"`
	ClosedBy string `json:"closed_by,omitempty"` // who ran team down; "" the admin
}

// MemberState is a participant of an open team (members that left it are not listed), in the
// order they entered it.
type MemberState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	State       string `json:"state"`
	Headless    bool   `json:"headless"`
	Gate        bool   `json:"gate"`
	StateSince  int64  `json:"state_since"`
	LastTurnEnd int64  `json:"last_turn_end,omitempty"`
	Unacked     int    `json:"unacked"`
	// Model is what its session last reported, else the model stored for the worker; "" unknown.
	Model string `json:"model,omitempty"`
	// Thinking is its thinking level the same way (session's report, else stored); "" unknown.
	Thinking string `json:"thinking,omitempty"`
	RunID    string `json:"run_id"` // current run: a headless worker's driver log is per run
	// ReportsTo is who gets its mail and handbacks now (moves with a reroute); "" none.
	ReportsTo string `json:"reports_to,omitempty"`
	// Capabilities is what its harness supports (Cap*); top shows ctx, turns and tail by them.
	// nil = not declared.
	Capabilities []string `json:"capabilities"`
	Harness      string   `json:"harness,omitempty"` // pi, claude, codex; "" unknown
	CreatedAt    int64    `json:"created_at"`        // when it joined or was spawned
	LastActivity int64    `json:"last_activity"`
	Cwd          string   `json:"cwd"`
	SpawnedBy    string   `json:"spawned_by,omitempty"` // participant id; "" joined on its own
	// ProtocolVersion is what its adapter sent at its latest identify; nil = none yet.
	ProtocolVersion *int `json:"protocol_version,omitempty"`
	// Transcript is its session's own file as its adapter reported it (the CLI reads ctx, turns
	// and tail from it); nil for a headless worker (its driver log) or none reported.
	Transcript *Transcript `json:"transcript,omitempty"`
	// Assignment is the latest mail marked op assign to it (a spawn or resume task is one); nil = none.
	Assignment *Assignment `json:"assignment,omitempty"`
}

// Assignment is a member's current task: a stored mail, not a field anyone reports.
type Assignment struct {
	Seq   int64  `json:"seq"`
	Title string `json:"title"` // the first non-empty line of the body, plain, capped
	From  string `json:"from"`  // the sender's name; "admin" for the admin's resume
	At    int64  `json:"at"`
	// Latest is the newest mail in the reply chain rooted at the assignment (mails whose reply_to
	// leads back to it); nil = the assignment itself is the newest.
	Latest *ChainMail `json:"latest,omitempty"`
	// Newer is set when the chain ends with the member's own mail and the assigner has since sent
	// the member a mail outside the chain (a task whose op assign was forgotten, or a note): the
	// newest such mail.
	Newer *NewerMail `json:"newer,omitempty"`
}

type ChainMail struct {
	Seq      int64 `json:"seq"`
	At       int64 `json:"at"`
	ByMember bool  `json:"by_member"` // the member sent it (a handback or a question); else it is live again
}

type NewerMail struct {
	Seq   int64  `json:"seq"`
	Title string `json:"title"`
	At    int64  `json:"at"`
}

var mdPairs = regexp.MustCompile("\\*\\*([^*]+)\\*\\*|__([^_]+)__|`([^`]+)`")

// assignmentTitle is body's first non-empty line without Markdown markers, at most 100 runes.
func assignmentTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(mdPairs.ReplaceAllString(strings.TrimSpace(line), "$1$2$3"), "#>-*+ \t"))
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > 100 {
			line = string(r[:100]) + "…"
		}
		return line
	}
	return ""
}

// SoloState is a live solo session.
type SoloState struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Cwd          string `json:"cwd"`
	State        string `json:"state"`
	StateSince   int64  `json:"state_since"`
	Unacked      int    `json:"unacked"`
	CreatedAt    int64  `json:"created_at"`            // when it joined
	GateClosed   bool   `json:"gate_closed,omitempty"` // its gate is closed to other teams and solos
	Harness      string `json:"harness,omitempty"`
	Model        string `json:"model,omitempty"` // as for a member: its session's report, else stored
	LastActivity int64  `json:"last_activity"`
	// LastTurnEnd is when its latest turn ended (unix ms), as for a member; 0 = none yet.
	LastTurnEnd int64 `json:"last_turn_end,omitempty"`
	// ProtocolVersion is what its adapter sent at its latest identify; nil = none yet.
	ProtocolVersion *int `json:"protocol_version,omitempty"`
	// Transcript is as for a member.
	Transcript *Transcript `json:"transcript,omitempty"`
}

// pendingMail counts, per recipient, mail not yet acked: [unacked (deliverable), held].
const pendingMail = `SELECT to_id, SUM(held_reason IS NULL), SUM(held_reason IS NOT NULL) FROM messages
	WHERE acked_at IS NULL AND to_id NOT IN ('notify', 'engine') GROUP BY to_id`

// State returns the operator snapshot.
func (e *Engine) State(ctx context.Context, a StateArgs) (State, error) {
	if a.Events <= 0 {
		a.Events = 10
	}
	out := State{Teams: []TeamState{}, Closed: []ClosedTeam{}, Solos: []SoloState{}, Events: []Event{}, Names: map[string]string{},
		ProtocolVersion: ProtocolVersion}
	err := e.readOnly(ctx, func(t *txn) error {
		pending := map[string][2]int{}
		rows, err := t.QueryContext(t.ctx, pendingMail)
		if err != nil {
			return internal(err)
		}
		for rows.Next() {
			var to string
			var n [2]int
			if err := rows.Scan(&to, &n[0], &n[1]); err != nil {
				rows.Close()
				return internal(err)
			}
			pending[to] = n
			out.Unacked += n[0]
			out.Held += n[1]
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return internal(err)
		}

		gateOf := map[string]string{} // team -> its gate's participant id
		teams, err := t.QueryContext(t.ctx, `SELECT id, name, template_name, root_cwd, created_at, COALESCE(parent_id,''),
			COALESCE((SELECT name FROM participants WHERE id=teams.parent_id),''), gate_closed, COALESCE(gate_id,'') FROM teams WHERE closed_at IS NULL ORDER BY name`)
		if err != nil {
			return internal(err)
		}
		for teams.Next() {
			var ts TeamState
			var gate string
			if err := teams.Scan(&ts.ID, &ts.Name, &ts.Template, &ts.Root, &ts.CreatedAt, &ts.ParentID, &ts.Parent, &ts.GateClosed, &gate); err != nil {
				teams.Close()
				return internal(err)
			}
			gateOf[ts.ID] = gate
			out.Teams = append(out.Teams, ts)
		}
		teams.Close()
		if err := teams.Err(); err != nil {
			return internal(err)
		}
		// Closed teams gc has not removed yet, newest first, with who closed them.
		closed, err := t.QueryContext(t.ctx, `SELECT id, name, template_name, root_cwd, closed_at, created_at, COALESCE(parent_id,''),
			COALESCE((SELECT name FROM participants WHERE id=teams.parent_id),''), COALESCE(gate_id,''),
			COALESCE((SELECT p.name FROM events e JOIN participants p ON p.id=e.participant
				WHERE e.type='team_down' AND e.team_id=teams.id ORDER BY e.seq DESC LIMIT 1),'') FROM teams
			WHERE closed_at IS NOT NULL ORDER BY closed_at DESC`)
		if err != nil {
			return internal(err)
		}
		for closed.Next() {
			var c ClosedTeam
			var gate string
			if err := closed.Scan(&c.ID, &c.Name, &c.Template, &c.Root, &c.ClosedAt, &c.CreatedAt, &c.ParentID, &c.Parent, &gate, &c.ClosedBy); err != nil {
				closed.Close()
				return internal(err)
			}
			gateOf[c.ID] = gate
			out.Closed = append(out.Closed, c)
		}
		closed.Close()
		if err := closed.Err(); err != nil {
			return internal(err)
		}
		// Members, gates and assignments of every team, open and closed, one query each (not one per team).
		var all []*TeamState
		for i := range out.Teams {
			all = append(all, &out.Teams[i])
		}
		for i := range out.Closed {
			all = append(all, &out.Closed[i].TeamState)
		}
		if err := t.fillTeams(all, gateOf, pending); err != nil {
			return err
		}

		solos, err := t.QueryContext(t.ctx, `SELECT `+participantCols+`, cwd, created_at, COALESCE(session_model, model, ''), protocol_version,
			transcript, transcript_format, last_turn_end, gate_closed FROM participants
			WHERE team_id IS NULL AND state<>'gone' ORDER BY created_at, rowid`)
		if err != nil {
			return internal(err)
		}
		for solos.Next() {
			var cwd, model string
			var created int64
			var proto, turn sql.NullInt64
			var tpath, tformat sql.NullString
			var shut bool
			q, err := scanParticipant(solos, &cwd, &created, &model, &proto, &tpath, &tformat, &turn, &shut)
			if err != nil {
				solos.Close()
				return internal(err)
			}
			out.Solos = append(out.Solos, SoloState{ID: q.id, Name: q.name, Cwd: cwd, State: q.state,
				StateSince: q.stateSince, Unacked: pending[q.id][0], CreatedAt: created, Harness: q.harness, Model: model,
				LastActivity: q.lastActivity, LastTurnEnd: turn.Int64, GateClosed: shut, ProtocolVersion: intOrNil(proto), Transcript: transcriptOrNil(tpath, tformat)})
		}
		solos.Close()
		if err := solos.Err(); err != nil {
			return internal(err)
		}

		evs, err := t.QueryContext(t.ctx, `SELECT seq, ts, type, COALESCE(participant,''), COALESCE(team_id,''),
			COALESCE(run_id,''), COALESCE(ref_id,''), payload FROM events ORDER BY seq DESC LIMIT ?`, a.Events)
		if err != nil {
			return internal(err)
		}
		defer evs.Close()
		for evs.Next() {
			var ev Event
			var payload string
			if err := evs.Scan(&ev.Seq, &ev.Ts, &ev.Type, &ev.Participant, &ev.TeamID, &ev.RunID, &ev.RefID, &payload); err != nil {
				return internal(err)
			}
			ev.Payload = []byte(payload)
			out.Events = append(out.Events, ev)
		}
		if err := evs.Err(); err != nil {
			return internal(err)
		}
		evs.Close()
		slices.Reverse(out.Events)
		names, err := t.eventNames(out.Events)
		if err != nil {
			return err
		}
		out.Names = names
		return nil
	})
	return out, err
}

// inList is "?,?,?" and the values as args, for an IN (...) over a batch.
func inList[T any](vals []T) (string, []any) {
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(vals)), ","), args
}

// fillTeams fills each team's gate and members (those that have not left, in the order they entered)
// and its mail counts: every row of the team counts (a leaver's held mail too). The rows of all
// teams come from one query, then are dealt out by team: a snapshot of many teams is a handful of
// queries, not several for every team and member (each query wakes the CPU).
func (t *txn) fillTeams(teams []*TeamState, gateOf map[string]string, pending map[string][2]int) error {
	byID := make(map[string]*TeamState, len(teams))
	for _, ts := range teams {
		ts.Members = []MemberState{}
		byID[ts.ID] = ts
	}
	assigned, err := t.assignments()
	if err != nil {
		return err
	}
	rows, err := t.QueryContext(t.ctx, `SELECT `+participantCols+`, COALESCE(mode,''), last_turn_end, left_at,
		COALESCE(session_model, model, ''), COALESCE(session_thinking, thinking, ''), COALESCE(capabilities, 'null'),
		created_at, COALESCE(spawned_by, ''), cwd, protocol_version, transcript, transcript_format
		FROM participants WHERE team_id IS NOT NULL ORDER BY `+joinOrder)
	if err != nil {
		return internal(err)
	}
	defer rows.Close()
	gateName := map[string]string{} // team -> the name of its gate, found among the team's own rows
	for rows.Next() {
		var mode, model, thinking, caps, spawnedBy, cwd string
		var turn, left, proto sql.NullInt64
		var created int64
		var tpath, tformat sql.NullString
		q, err := scanParticipant(rows, &mode, &turn, &left, &model, &thinking, &caps, &created, &spawnedBy, &cwd, &proto,
			&tpath, &tformat)
		if err != nil {
			return internal(err)
		}
		ts := byID[q.team]
		if ts == nil {
			continue
		}
		if q.id == gateOf[q.team] {
			gateName[q.team] = q.name
		}
		n := pending[q.id]
		ts.Unacked += n[0]
		ts.Held += n[1]
		if left.Valid {
			continue
		}
		ts.Members = append(ts.Members, MemberState{ID: q.id, Name: q.name, Role: q.role, State: q.state,
			Headless: mode == modeHeadless, Gate: q.id == gateOf[q.team], StateSince: q.stateSince,
			LastTurnEnd: turn.Int64, Unacked: n[0], Model: model, Thinking: thinking, RunID: q.run, ReportsTo: q.reportsTo,
			CreatedAt: created, SpawnedBy: spawnedBy, Harness: q.harness, LastActivity: q.lastActivity, Cwd: cwd,
			ProtocolVersion: intOrNil(proto), Transcript: transcriptOrNil(tpath, tformat), Assignment: assigned[q.id]})
		json.Unmarshal([]byte(caps), &ts.Members[len(ts.Members)-1].Capabilities)
	}
	if err := rows.Err(); err != nil {
		return internal(err)
	}
	rows.Close()
	for team, id := range gateOf {
		ts := byID[team]
		if ts == nil || id == "" {
			continue
		}
		if name, ok := gateName[team]; ok {
			ts.Gate = name
		} else if g, ok, err := t.participantByID(id); err != nil { // a gate that moved out of the team
			return err
		} else if ok {
			ts.Gate = g.name
		}
	}
	return nil
}

// assignments returns, per member (of any team), its latest delivered mail marked op assign, with
// the state of that assignment's reply chain (see Assignment): three queries for all of them.
func (t *txn) assignments() (map[string]*Assignment, error) {
	rows, err := t.QueryContext(t.ctx, `SELECT m.to_id, m.id, m.seq, m.body, m.from_id, COALESCE(f.name, 'admin'), m.created_at
		FROM messages m LEFT JOIN participants f ON f.id=m.from_id
		WHERE m.op=? AND m.held_reason IS NULL AND m.to_id IN (SELECT id FROM participants WHERE team_id IS NOT NULL)
		AND m.seq=(SELECT MAX(x.seq) FROM messages x WHERE x.op=m.op AND x.to_id=m.to_id AND x.held_reason IS NULL)`,
		OpAssign)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	out := map[string]*Assignment{}
	from := map[string]string{} // member -> assigner's id
	ids := map[string]string{}  // member -> assignment message id
	for rows.Next() {
		var to, id, body, fromID string
		var a Assignment
		if err := rows.Scan(&to, &id, &a.Seq, &body, &fromID, &a.From, &a.At); err != nil {
			return nil, internal(err)
		}
		a.Title = assignmentTitle(body)
		out[to], from[to], ids[to] = &a, fromID, id
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	// The newest mail of each assignment's reply chain (the mails whose reply_to leads back to it).
	root := map[string]string{} // assignment message id -> member
	var rootIDs []string
	for to, id := range ids {
		root[id] = to
		rootIDs = append(rootIDs, id)
	}
	in, args := inList(rootIDs)
	crows, err := t.QueryContext(t.ctx, `WITH RECURSIVE chain(root, id) AS (
			SELECT id, id FROM messages WHERE id IN (`+in+`)
			UNION SELECT chain.root, m.id FROM messages m JOIN chain ON m.reply_to=chain.id)
		SELECT chain.root, m.seq, m.created_at, m.from_id FROM chain JOIN messages m ON m.id=chain.id WHERE chain.id<>chain.root`, args...)
	if err != nil {
		return nil, internal(err)
	}
	defer crows.Close()
	latest := map[string]ChainMail{} // member -> newest mail of the chain
	sender := map[string]string{}
	for crows.Next() {
		var rootID, by string
		var c ChainMail
		if err := crows.Scan(&rootID, &c.Seq, &c.At, &by); err != nil {
			return nil, internal(err)
		}
		to := root[rootID]
		if cur, ok := latest[to]; !ok || c.Seq > cur.Seq {
			latest[to], sender[to] = c, by
		}
	}
	if err := crows.Err(); err != nil {
		return nil, internal(err)
	}
	crows.Close()
	// Where the chain ends with the member's own mail: the assigner's newest mail to the member since
	// that is not in the chain's reach (a note, or a task whose op assign was forgotten).
	var vals []string
	var vargs []any
	for to, c := range latest {
		c.ByMember = sender[to] == to
		a := out[to]
		l := c
		a.Latest = &l
		if c.ByMember {
			vals = append(vals, "(?,?,?,?)")
			vargs = append(vargs, to, from[to], to, c.Seq)
		}
	}
	if len(vals) == 0 {
		return out, nil
	}
	nrows, err := t.QueryContext(t.ctx, `WITH v(k, f, t, s) AS (VALUES `+strings.Join(vals, ",")+`)
		SELECT v.k, m.seq, m.body, m.created_at FROM v JOIN messages m
			ON m.from_id=v.f AND m.to_id=v.t AND m.seq>v.s AND m.cc_of IS NULL AND m.held_reason IS NULL
		WHERE m.seq=(SELECT MAX(x.seq) FROM messages x WHERE x.from_id=v.f AND x.to_id=v.t AND x.seq>v.s
			AND x.cc_of IS NULL AND x.held_reason IS NULL)`, vargs...)
	if err != nil {
		return nil, internal(err)
	}
	defer nrows.Close()
	for nrows.Next() {
		var to, body string
		var n NewerMail
		if err := nrows.Scan(&to, &n.Seq, &body, &n.At); err != nil {
			return nil, internal(err)
		}
		n.Title = assignmentTitle(body)
		out[to].Newer = &n
	}
	return out, internal(nrows.Err())
}

// eventNames labels every id in evs (actor or ref): a participant by its name, a message by its #seq.
// A team ref has none.
func (t *txn) eventNames(evs []Event) (map[string]string, error) {
	names := map[string]string{}
	var ids []string
	for _, ev := range evs {
		for _, id := range []string{ev.Participant, ev.RefID} {
			if id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return names, nil
	}
	in, args := inList(ids)
	rows, err := t.QueryContext(t.ctx, `SELECT id, name FROM participants WHERE id IN (`+in+`)
		UNION ALL SELECT id, '#' || seq FROM messages WHERE id IN (`+in+`)`, append(args, args...)...)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	for rows.Next() { // a participant first, then a message (an id is only one of them)
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, internal(err)
		}
		if _, done := names[id]; !done {
			names[id] = name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	return names, nil
}

func transcriptOrNil(path, format sql.NullString) *Transcript {
	if !path.Valid {
		return nil
	}
	return &Transcript{Path: path.String, Format: format.String}
}

func intOrNil(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

type WorkerLogArgs struct {
	Worker string `json:"worker"`         // name or participant id
	Team   string `json:"team,omitempty"` // when the name is in more than one team
}

// WorkerLog names the run of a worker the daemon started and that is its run now; its driver log is
// the tail.
type WorkerLog struct {
	ParticipantID string `json:"participant_id"`
	RunID         string `json:"run_id"` // a worker's current run ("" for a session)
	// Transcript is a session's own file (no process of piggery's), as its adapter reported it.
	Transcript *Transcript `json:"transcript,omitempty"`
}

// WorkerLog resolves a worker to its latest run, or a session to its transcript. Read-only.
func (e *Engine) WorkerLog(ctx context.Context, a WorkerLogArgs) (WorkerLog, error) {
	var out WorkerLog
	err := e.readOnly(ctx, func(t *txn) error {
		p, err := t.participantForAdmin(a.Worker, a.Team)
		if err != nil {
			return err
		}
		out.ParticipantID = p.id
		// Only a process of the participant's current run: one that was a headless worker and is
		// now a session (a resume in a TUI) has its old driver log, not what it does now.
		err = t.QueryRowContext(t.ctx, `SELECT pr.run_id FROM processes pr JOIN participants q ON q.id=pr.participant_id
			AND q.run_id=pr.run_id WHERE pr.participant_id=? ORDER BY pr.started_at DESC LIMIT 1`, p.id).Scan(&out.RunID)
		if !errors.Is(err, sql.ErrNoRows) {
			return internal(err)
		}
		var path, format sql.NullString
		if err := t.QueryRowContext(t.ctx, `SELECT transcript, transcript_format FROM participants WHERE id=?`,
			p.id).Scan(&path, &format); err != nil {
			return internal(err)
		}
		if out.Transcript = transcriptOrNil(path, format); out.Transcript == nil {
			return errf(CodeNotFound, "%s has no log: not a worker, and its harness reported no transcript", a.Worker)
		}
		return nil
	})
	return out, err
}
