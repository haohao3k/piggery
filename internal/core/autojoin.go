package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// JoinAutoArgs is `join.auto`: a harness session with no PIGGERY_* registers with the daemon. No
// token: the socket's 0600 mode is the boundary.
type JoinAutoArgs struct {
	Cwd        string `json:"cwd"`
	Harness    string `json:"harness,omitempty"`
	Mode       string `json:"mode,omitempty"`
	HarnessRef string `json:"harness_ref"`    // same session -> same participant
	Name       string `json:"name,omitempty"` // default one word from harness_ref (names.go); suffixed on collision
	// Source is why the harness started this session (Claude: startup|resume|clear|compact);
	// recorded only. A Codex thread is never inferred from its process or this source.
	Source string `json:"source,omitempty"`
	// Host is the harness process the session lives in ("claude:<pid>:<start time>"), shared by
	// its hooks and MCP server. For Claude, a live participant with the same host is this session: join.auto
	// returns it with its run (the ref becomes one of its refs), and callers may authenticate by
	// host (AuthenticateHost). Codex app-server shares a process across threads: it requires
	// the exact HarnessRef and canonical Cwd as well (AuthenticateSession).
	Host string `json:"host,omitempty"`
	// Transcript is the harness's own file of this session, kept on the participant for the CLI's
	// ctx, turns and tail (never read by the daemon). nil or an empty path keeps the one stored.
	Transcript *Transcript `json:"transcript,omitempty"`
}

// Transcript is a session's own file as its harness writes it, and the format a CLI reader knows
// it by (pi, claude, codex). Core stores and returns it as sent.
type Transcript struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

// transcriptCols are a.Transcript's path and format for an UPDATE with COALESCE: NULL when none
// was reported, so the stored one stays.
func (a JoinAutoArgs) transcriptCols() (any, any) {
	if a.Transcript == nil || a.Transcript.Path == "" {
		return nil, nil
	}
	return a.Transcript.Path, a.Transcript.Format
}

// JoinAuto registers session a. A session whose newest participant is a solo or a member of an open
// team resumes it (same role and name, new run and token), only if it is gone and not a headless
// worker (live -> CodeInvalid, headless -> CodeNotFound). Otherwise the session is a new solo: a
// participant with no team, whatever its cwd. TeamID "" = solo.
func (e *Engine) JoinAuto(ctx context.Context, a JoinAutoArgs) (JoinResult, error) {
	if a.HarnessRef == "" || a.Mode == modeHeadless {
		return JoinResult{}, errf(CodeInvalid, "harness_ref is required; mode headless is only for spawned workers")
	}
	cwd, err := normalizeCwd(a.Cwd)
	if err != nil {
		return JoinResult{}, err
	}
	token, err := newToken()
	if err != nil {
		return JoinResult{}, internal(err)
	}
	var res JoinResult
	err = e.inTx(ctx, func(t *txn) error {
		codex := a.Harness == "codex" || strings.HasPrefix(a.Host, "codex:")
		if codex && a.Host != "" {
			// A process is only transport provenance. Multiple Codex chats, including chats
			// in the same directory, must have different participants and return addresses.
			var id, team, run, storedCwd string
			err := t.QueryRowContext(t.ctx, `SELECT id, COALESCE(team_id,''), run_id, cwd FROM participants
				WHERE host=? AND session_ref=? AND binding_quarantined=0
				AND state<>'gone' AND left_at IS NULL AND COALESCE(mode,'')<>'headless'
				ORDER BY created_at DESC, rowid DESC LIMIT 1`, a.Host, a.HarnessRef).Scan(&id, &team, &run, &storedCwd)
			if err == nil {
				if storedCwd != cwd {
					return sessionRootMismatch()
				}
				res = JoinResult{ID: id, TeamID: team, RunID: run}
				path, format := a.transcriptCols()
				_, err = t.ExecContext(t.ctx, `UPDATE participants SET transcript=COALESCE(?,transcript),
					transcript_format=COALESCE(?,transcript_format) WHERE id=?`, path, format, id)
				return internal(err)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return internal(err)
			}
		}
		if a.Host != "" && !codex { // Claude's process survives /clear; Codex has independent threads.
			var id, team, run, storedCwd string
			err := t.QueryRowContext(t.ctx, `SELECT id, COALESCE(team_id,''), run_id, cwd FROM participants WHERE host=?
				AND state<>'gone' AND left_at IS NULL AND COALESCE(mode,'')<>'headless'
				ORDER BY created_at DESC, rowid DESC LIMIT 1`, a.Host).Scan(&id, &team, &run, &storedCwd)
			if err == nil {
				if storedCwd != cwd {
					return sessionRootMismatch()
				}
				res = JoinResult{ID: id, TeamID: team, RunID: run} // no token: it authenticates by host
				path, format := a.transcriptCols()
				if _, err := t.ExecContext(t.ctx, `UPDATE participants SET session_ref=?, transcript=COALESCE(?,transcript),
					transcript_format=COALESCE(?,transcript_format) WHERE id=?`, a.HarnessRef, path, format, id); err != nil {
					return internal(err)
				}
				_, err := t.ExecContext(t.ctx, `INSERT INTO participant_refs(ref, participant_id) SELECT ?, ?
					WHERE ? <> COALESCE((SELECT harness_ref FROM participants WHERE id=?),'') ON CONFLICT(ref) DO NOTHING`,
					a.HarnessRef, id, a.HarnessRef, id)
				return internal(err)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return internal(err)
			}
		}
		res = JoinResult{Token: token, RunID: newID(t.now)}
		// The newest row wins: after leaving a team (found) the old row is gone in the old team.
		// A row that left (found elsewhere, replaced or merged by reopen) never comes back.
		var mode, host sql.NullString
		p, err := scanParticipant(t.QueryRowContext(t.ctx, `SELECT `+participantCols+`, mode, host FROM participants
			WHERE (harness_ref=? OR id IN (SELECT participant_id FROM participant_refs WHERE ref=?)) AND left_at IS NULL
			AND binding_quarantined=0
			AND (team_id IS NULL OR team_id IN (SELECT id FROM teams WHERE closed_at IS NULL))
			ORDER BY created_at DESC, rowid DESC LIMIT 1`, a.HarnessRef, a.HarnessRef), &mode, &host)
		switch {
		case err == nil && mode.String == "headless":
			// A worker's session opened by hand (e.g. to inspect it) must never take the worker over.
			return errf(CodeNotFound, "session belongs to a headless worker")
		case err == nil && p.state != "gone" && (a.Host == "" || !host.Valid):
			// Only a session that is gone can come back; a live one keeps its run and token. A
			// live one with another host lost its process without saying so (it reported no
			// end): the new process takes it over, as a resume.
			return errf(CodeInvalid, "participant is live")
		case err == nil: // resume: same participant, new run and token
			if codex {
				var storedCwd, storedRef string
				if err := t.QueryRowContext(t.ctx, `SELECT cwd, COALESCE(session_ref,harness_ref,'') FROM participants WHERE id=?`, p.id).Scan(&storedCwd, &storedRef); err != nil {
					return internal(err)
				}
				if storedCwd != cwd {
					return sessionRootMismatch()
				}
				if storedRef != a.HarnessRef {
					return &Error{Code: CodeUnauthorized, RuleID: "session.ref", Layer: "token", Message: "a different Codex thread cannot resume this participant"}
				}
			}
			res.ID, res.TeamID = p.id, p.team
			path, format := a.transcriptCols()
			if _, err := t.ExecContext(t.ctx, `UPDATE participants SET run_id=?, token_hash=?, last_turn_end=NULL,
				cwd=?, harness=COALESCE(?,harness), mode=COALESCE(?,mode), host=?, session_ref=?,
				transcript=COALESCE(?,transcript), transcript_format=COALESCE(?,transcript_format) WHERE id=?`,
				res.RunID, hashToken(token), cwd, nullStr(a.Harness), nullStr(a.Mode), nullStr(a.Host), a.HarnessRef,
				path, format, p.id); err != nil {
				return internal(err)
			}
			p.run = res.RunID
			return t.setState(&p, "idle", "join.auto")
		case !errors.Is(err, sql.ErrNoRows):
			return internal(err)
		}
		res.ID, err = t.insertSession("", "", cwd, res.RunID, token, a)
		return err
	})
	if err != nil {
		return JoinResult{}, err
	}
	return res, nil
}

// moveSession hands what a live session reported (its host, capabilities, tool prefix, model
// and thinking, current session id, transcript) and its other session ids from participant from to participant to, when the
// session goes on as another participant (found by a member, reopen by the old gate). Without
// it, auth by host would find nobody and resuming a /clear'd id would miss the new row.
func (t *txn) moveSession(from, to string) error {
	if _, err := t.ExecContext(t.ctx, `UPDATE participants SET (host, capabilities, tool_prefix, session_model, session_thinking,
		session_ref, transcript, transcript_format) = (SELECT host, capabilities, tool_prefix, session_model, session_thinking,
		session_ref, transcript, transcript_format FROM participants WHERE id=?) WHERE id=?`,
		from, to); err != nil {
		return internal(err)
	}
	if _, err := t.ExecContext(t.ctx, `UPDATE participants SET host=NULL WHERE id=?`, from); err != nil {
		return internal(err)
	}
	_, err := t.ExecContext(t.ctx, `UPDATE participant_refs SET participant_id=? WHERE participant_id=?`, to, from)
	return internal(err)
}

// insertSession adds harness session a as a new idle participant of role in teamID ("" = a
// solo, no team and no role), named a.Name (suffixed on collision) or else a free word
// (freeWord), and returns its id.
func (t *txn) insertSession(teamID, role, cwd, run, token string, a JoinAutoArgs) (string, error) {
	var name string
	var err error
	if n := strings.TrimSpace(a.Name); n != "" && !isReserved(n) {
		name, err = t.freeName(teamID, n)
	} else {
		name, err = t.freeWord(teamID, a.HarnessRef)
	}
	if err != nil {
		return "", err
	}
	id := newID(t.now)
	path, format := a.transcriptCols()
	if _, err := t.ExecContext(t.ctx, `INSERT INTO participants
		(id, run_id, kind, harness, mode, name, cwd, team_id, role, state, state_since, last_activity,
		 token_hash, harness_ref, created_at, host, session_ref, transcript, transcript_format)
		VALUES (?,?,'agent',?,?,?,?,?,?,'idle',?,?,?,?,?,?,?,?,?)`,
		id, run, nullStr(a.Harness), nullStr(a.Mode), name, cwd, nullStr(teamID), nullStr(role), t.now, t.now,
		hashToken(token), a.HarnessRef, t.now, nullStr(a.Host), a.HarnessRef, path, format); err != nil {
		return "", internal(err)
	}
	return id, nil
}

// sessionRole is the role a harness session joins as: auto_join_role, else the only role, else "".
func (m manifest) sessionRole() string {
	if m.AutoJoinRole != "" || len(m.Roles) != 1 {
		return m.AutoJoinRole
	}
	for only := range m.Roles {
		return only
	}
	return ""
}

// freeName returns name, or name-2, name-3, … when taken in the team (teamID "": by a live
// solo).
func (t *txn) freeName(teamID, name string) (string, error) {
	for i := 1; ; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s-%d", name, i)
		}
		taken, err := t.nameTaken(teamID, cand, false)
		if err != nil || !taken {
			return cand, err
		}
	}
}

// nameTaken: name is a participant's in the team (teamID "": a live solo's), or, with teams,
// an open team's (a send to it would reach that team: resolveSendTarget).
func (t *txn) nameTaken(teamID, name string, teams bool) (bool, error) {
	var n int
	q := `SELECT (SELECT COUNT(*) FROM participants WHERE team_id=? AND name=?)`
	args := []any{teamID, name}
	if teamID == "" {
		q, args = `SELECT (SELECT COUNT(*) FROM participants WHERE team_id IS NULL AND state<>'gone' AND name=?)`, []any{name}
	}
	if teams {
		q += ` + (SELECT COUNT(*) FROM teams WHERE closed_at IS NULL AND name=?)`
		args = append(args, name)
	}
	if err := t.QueryRowContext(t.ctx, q, args...).Scan(&n); err != nil {
		return false, internal(err)
	}
	return n > 0, nil
}
