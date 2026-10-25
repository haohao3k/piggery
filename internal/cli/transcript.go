package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/internal/proto"
)

// transcriptReader reads one kind of log a record (line) at a time: a headless worker's driver
// log, or an interactive session's own transcript as its harness writes it (core.Transcript,
// reported by its adapter). top, ps and tail read every log through one, so a session shows ctx,
// turns and a tail the way a worker does. Each session format lives in its own file
// (transcript_<format>.go) and registers itself in formats. A record a reader does not understand
// gives a zero record: these files are the harnesses' own, not a stable interface, and a reader
// follows its harness when the format changes.
type transcriptReader interface {
	// read is called once per record, in file order, on one reader per file, so a reader may
	// keep state across records (message ids seen, a tool's name by its call id).
	read(rec []byte) record
}

// resumable is a reader whose state matters for ctx and turns (not only for tail lines): its state
// is saved with the offset (logcache.go), so a later call reads on from there with the same result
// as reading the file from the start.
type resumable interface {
	save() json.RawMessage
	load(json.RawMessage)
}

// record is what one record says.
type record struct {
	ctx     int    // the context in tokens now, when hasCtx (the latest such record wins)
	hasCtx  bool   //
	turnEnd bool   // it ends an assistant turn (turns counts them; each turn once)
	turns   int    // turns it counts, as turnEnd counts one (a driver log's turn_end, or its trimmed record: many)
	line    string // it as one tail line, in tailLine's prefixes ("> ", "< ", "assistant: ", "! ", "-- "); "" none
}

// driverLog is the format of a headless worker's driver log (driver/local.LogPath): pi rpc
// records, as the local driver writes them for every harness it runs.
const driverLog = "driver"

// formats make a reader for one file, by format name: driverLog, and each session format by the
// name its adapter reports (transcript_<format>.go adds it in an init). An unknown name has no
// reader: its session shows "-".
var formats = map[string]func() transcriptReader{driverLog: func() transcriptReader { return driverReader{} }}

type driverReader struct{}

func (driverReader) read(rec []byte) record {
	var r record
	r.turns, r.ctx, r.hasCtx = local.RecordUsage(rec)
	r.line, _ = tailLine(rec)
	return r
}

// scan adds what recs (new records of one file) say to st, through st's reader for that file
// (made from newReader on the first call, with the state saved in the cache if any): the latest
// context, and turns.
func scan(newReader func() transcriptReader, recs [][]byte, st logState) logState {
	if st.reader == nil {
		st.reader = newReader()
		if r, ok := st.reader.(resumable); ok && st.state != nil {
			r.load(st.state)
		}
	}
	for _, rec := range recs {
		r := st.reader.read(rec)
		if r.hasCtx {
			st.tokens, st.seen = r.ctx, true
		}
		if r.turnEnd {
			st.turns++
		}
		st.turns += r.turns
	}
	return st
}

// sessionReader is the reader of session transcript t, false when there is none or its format has
// no reader here (the session then shows "-").
func sessionReader(t *core.Transcript) (func() transcriptReader, bool) {
	if t == nil || t.Path == "" {
		return nil, false
	}
	f, ok := formats[t.Format]
	return f, ok
}

// tailSource is where the tail verb's answer r is read: a worker's driver log (its latest run), or
// a session's transcript.
func tailSource(r proto.TailResult) (string, func() transcriptReader, error) {
	if r.RunID != "" {
		return r.Path, formats[driverLog], nil
	}
	f, ok := sessionReader(r.Transcript)
	if !ok {
		return "", nil, fmt.Errorf("no log to read for this session (transcript %+v)", r.Transcript)
	}
	return r.Transcript.Path, f, nil
}

// markLen is how many bytes before a log's offset its mark covers.
const markLen = 256

// mark is a fingerprint of the markLen bytes of f before off: the same file read on from off has
// the same bytes there; a rewritten one (same or larger size) almost never does.
func mark(f *os.File, off int64) string {
	start := max(0, off-markLen)
	b := make([]byte, off-start)
	if _, err := f.ReadAt(b, start); err != nil && err != io.EOF {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// advanceLog brings st (what was read of the log at path) up to date, reading only the bytes it
// gained: false when the file is missing or unreadable (its participant shows "-"). A file shorter
// than what was read, or whose bytes before the offset changed (pi rewrites its session file on
// some edits), is read again from the start.
func advanceLog(path string, newReader func() transcriptReader, st logState) (logState, bool) {
	f, err := os.Open(path)
	if err != nil {
		return st, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return st, false
	}
	if st.off > 0 && (fi.Size() < st.off || mark(f, st.off) != st.mark) {
		st = logState{}
	}
	recs, off, err := readRecords(path, st.off)
	if err != nil {
		return st, false
	}
	st.off = off
	st = scan(newReader, recs, st)
	st.mark = mark(f, st.off)
	return st, true
}

// tailWindow is how much of a log's end a tail reads first; doubled until it holds n lines or
// reaches the start.
const tailWindow = 256 << 10

// lastLines is the last n lines of the log at path (raw records when raw, else the readable ones,
// through a fresh reader), read back from the end, not from the start: a transcript can be tens of
// MB. off is where to follow on from, reader the one that read up to it. A reader that starts
// mid-file may not know a tool result's tool (it shows "< ok").
func lastLines(path string, newReader func() transcriptReader, n int, raw bool) (lines []string, off int64,
	reader transcriptReader, err error) {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, 0, newReader(), nil // the run has not written yet
	}
	if err != nil {
		return nil, 0, nil, err
	}
	for window := int64(tailWindow); ; window *= 2 {
		start := max(0, fi.Size()-window)
		var recs [][]byte
		if recs, off, err = readRecords(path, start); err != nil {
			return nil, 0, nil, err
		}
		if start > 0 && len(recs) > 0 && !lineStart(path, start) {
			recs = recs[1:] // the window began inside it
		}
		reader, lines = newReader(), nil
		for _, rec := range recs {
			l := reader.read(rec).line
			if raw {
				lines = append(lines, string(rec))
			} else if l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) >= n || start == 0 {
			return lines[max(0, len(lines)-n):], off, reader, nil
		}
	}
}

// lineStart reports whether off in the file at path begins a line.
func lineStart(path string, off int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 1)
	_, err = f.ReadAt(b, off-1)
	return err == nil && b[0] == '\n'
}
