-- v28 -> v29: ack per model turn. part numbers the gives of an adapter batch (turn_start's
-- is 1, each later give +1); an adapter's `seen` acks the parts a finished model turn read. NULL for
-- deliveries made before, and for gives outside an adapter turn.
ALTER TABLE deliveries ADD COLUMN part INTEGER;
