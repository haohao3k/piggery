-- v22 -> v23: quarantine legacy interactive Codex bindings whose stored thread aliases are
-- ambiguous. The rows, aliases and all message/run history remain in place; the core excludes the
-- row from future session/token lookup so a fresh exact thread binding is created instead.
ALTER TABLE participants ADD COLUMN binding_quarantined INTEGER NOT NULL DEFAULT 0;

UPDATE participants AS p
SET binding_quarantined = 1
WHERE (lower(COALESCE(p.harness, '')) = 'codex' OR lower(COALESCE(p.host, '')) LIKE 'codex:%')
  AND lower(COALESCE(p.mode, '')) <> 'headless'
  AND (
    COALESCE(p.session_ref, '') <> COALESCE(p.harness_ref, '')
    OR EXISTS (
      SELECT 1
      FROM participant_refs AS r
      WHERE r.participant_id = p.id
        AND r.ref <> COALESCE(p.harness_ref, '')
    )
  );
