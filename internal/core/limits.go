package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Mail storm limit. Only a limit declared in the manifest applies. A message over it is stored
// but held (held_reason policy_hold): not delivered, not woken for, never acked, until an admin
// releases it. Engine mail never goes through Send, so it is exempt and never causes a notice.
const (
	RuleRatePerMinute = "limits.messages_per_participant_per_minute"
	heldPolicy        = "policy_hold"
)

// mailHold returns the limit the new message breaks ("" = none) and the notice dedupe key: the
// rule and the minute window.
func (t *txn) mailHold(p participant, m manifest) (rule, key string, err error) {
	if limit, ok := m.Limits["messages_per_participant_per_minute"]; ok {
		var n int
		if err := t.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM messages WHERE from_id=? AND created_at>? AND cc_of IS NULL`,
			p.id, t.now-60_000).Scan(&n); err != nil {
			return "", "", internal(err)
		}
		if n >= limit {
			return RuleRatePerMinute, fmt.Sprintf("%s/%d", RuleRatePerMinute, t.now/60_000), nil
		}
	}
	return "", "", nil
}

// hold marks a just-written message held, with its event, and sends the sender one notice per
// dedupe key. It reports whether a notice was sent.
func (t *txn) hold(p participant, msgID, to, rule, key string) (bool, error) {
	if _, err := t.ExecContext(t.ctx, `UPDATE messages SET held_reason=? WHERE id=?`, heldPolicy, msgID); err != nil {
		return false, internal(err)
	}
	if err := t.event(evt{typ: "held", participant: p.id, team: p.team, run: p.run, ref: msgID,
		payload: map[string]any{"rule_id": rule, "reason": heldPolicy, "to": to}}); err != nil {
		return false, err
	}
	var seen int
	if err := t.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM events
		WHERE participant=? AND type='notice' AND json_extract(payload,'$.key')=?`, p.id, key).Scan(&seen); err != nil {
		return false, internal(err)
	}
	if seen > 0 {
		return false, nil
	}
	notice := newID(t.now)
	body := fmt.Sprintf("Your message %s is held by %s and was not delivered. Further messages over this limit "+
		"are held without another notice. Slow down; an admin can release it with "+
		"`piggery --admin release %s`.", msgID, rule, msgID)
	if _, err := t.insertMessage(notice, "", p.team, AddrEngine, p.id, "", "", "", body); err != nil {
		return false, err
	}
	return true, t.event(evt{typ: "notice", participant: p.id, team: p.team, ref: notice,
		payload: map[string]any{"rule_id": rule, "key": key, "message_id": msgID}})
}

// ReleaseArgs is the admin `release` verb.
type ReleaseArgs struct {
	ID string `json:"id"` // held message id
}

// Release (admin) clears a message's hold; it then delivers normally and its recipient is woken.
func (e *Engine) Release(ctx context.Context, a ReleaseArgs) error {
	var to string
	var copies []string // cc copy recipients
	err := e.inTx(ctx, func(t *txn) error {
		var team, from string
		var held sql.NullString
		var err error
		if a.ID, err = t.messageRef(a.ID); err != nil { // #N: the rest of Release uses the id
			return err
		}
		err = t.QueryRowContext(t.ctx, `SELECT COALESCE(team_id,''), from_id, to_id, held_reason FROM messages WHERE id=?`,
			a.ID).Scan(&team, &from, &to, &held)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "no message %q", a.ID)
		}
		if err != nil {
			return internal(err)
		}
		if !held.Valid {
			return errf(CodeInvalid, "message %s is not held", a.ID)
		}
		var closed int
		if err := t.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM teams WHERE id=? AND closed_at IS NOT NULL`,
			team).Scan(&closed); err != nil {
			return internal(err)
		}
		if closed > 0 {
			return errf(CodeInvalid, "message %s belongs to a closed team (team.closed)", a.ID)
		}
		// Its routing cc copies share the hold and are released with it.
		rows, err := t.QueryContext(t.ctx, `SELECT to_id FROM messages WHERE cc_of=? AND held_reason IS NOT NULL`, a.ID)
		if err != nil {
			return internal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return internal(err)
			}
			copies = append(copies, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return internal(err)
		}
		if _, err := t.ExecContext(t.ctx, `UPDATE messages SET held_reason=NULL WHERE id=? OR cc_of=?`, a.ID, a.ID); err != nil {
			return internal(err)
		}
		return t.event(evt{typ: "released", participant: from, team: team, ref: a.ID,
			payload: map[string]any{"reason": held.String, "to": to, "cc_copies": len(copies)}})
	})
	if err != nil {
		return err
	}
	for _, id := range copies {
		e.notifyAfterCommit(id)
	}
	if to == AddrNotify {
		e.notifyHookAfterCommit(a.ID)
	} else {
		e.notifyAfterCommit(to)
	}
	return nil
}
