-- v29 -> v30: the board is gone. The view of its live pins and the index of its mail go; the rows
-- stay in messages (to_id 'board', and op replace|remove with target), and so do the target column
-- and its index: gc still has to see them as references.
DROP VIEW IF EXISTS pins;
DROP INDEX IF EXISTS messages_board;
