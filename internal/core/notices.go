package core

import "context"

// Notices are the latest n messages to notify, newest first, as the notify hooks receive them
// (NotifyMail); top shows them. One bounded read of the messages table.
func (e *Engine) Notices(ctx context.Context, n int) ([]NotifyMail, error) {
	out := []NotifyMail{}
	err := e.inTx(ctx, func(t *txn) error {
		rows, err := t.QueryContext(t.ctx, `SELECT id FROM messages WHERE to_id=? ORDER BY created_at DESC, id DESC LIMIT ?`, AddrNotify, n)
		if err != nil {
			return internal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return internal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return internal(err)
		}
		rows.Close()
		for _, id := range ids {
			m, err := t.notifyMail(id)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}
