package local

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A worker's run logs (LogPath, and a .stderr next to each) only serve to look back and to add up
// what top shows (RecordUsage). A worker gone for good keeps only the end of its last run: gc
// calls TrimLogs for it, and what the dropped records said about usage stays in one record at the
// head of the file, so top shows the same numbers.
const (
	trimRecords = 1000    // records kept of the last run
	trimBytes   = 5 << 20 // and at most this many bytes of them, whichever is tighter
	trimStderr  = 64 << 10
)

// trimmedType is the record that stands for the ones TrimLogs dropped: their turns, and the last
// context they held. Readers other than RecordUsage skip it as a record they do not know.
const trimmedType = "piggery_trimmed"

type trimmed struct {
	Type  string `json:"type"`
	Turns int    `json:"turns,omitempty"`
	Ctx   *int   `json:"ctx,omitempty"`
}

var trimMu sync.Mutex

// RecordUsage is what one record of a run log adds to the usage top shows: turns (a turn_end is
// one; the trimmed record, those of the records it replaced), and the context in tokens of an
// assistant message_end with usage (its totalTokens, or input, output and cache together; or the
// last one the trimmed record kept). The latest context wins; turns add up.
func RecordUsage(rec []byte) (turns, ctx int, hasCtx bool) {
	var r struct {
		trimmed
		Message struct {
			Role  string
			Usage *struct {
				Input, Output, CacheRead, CacheWrite, TotalTokens int
			}
		}
	}
	if json.Unmarshal(rec, &r) != nil {
		return 0, 0, false
	}
	switch r.Type {
	case "turn_end":
		return 1, 0, false
	case trimmedType:
		if r.Ctx != nil {
			return r.Turns, *r.Ctx, true
		}
		return r.Turns, 0, false
	case "message_end":
		if r.Message.Role != "assistant" || r.Message.Usage == nil {
			return 0, 0, false
		}
		u := r.Message.Usage
		if u.TotalTokens > 0 {
			return 0, u.TotalTokens, true
		}
		return 0, u.Input + u.Output + u.CacheRead + u.CacheWrite, true
	}
	return 0, 0, false
}

// usageOf adds up RecordUsage over the records read from r.
func usageOf(r io.Reader) (turns, ctx int, hasCtx bool, err error) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, rerr := br.ReadBytes('\n')
		t, c, ok := RecordUsage(line)
		turns += t
		if ok {
			ctx, hasCtx = c, true
		}
		if rerr != nil {
			if rerr == io.EOF {
				rerr = nil
			}
			return turns, ctx, hasCtx, rerr
		}
	}
}

// TrimLogs cuts the run logs of participant key (<dir>/logs/<key>) down to the end of its run
// run: the files of other runs are deleted, the log of run keeps its last trimRecords records and
// at most trimBytes, cut at a line, and its .stderr the last trimStderr bytes. The turns of what
// goes, and the last context of the dropped records of run, are put in a record at the head of the
// kept file (RecordUsage). When no log is named run it is the newest one that stays. It returns
// the bytes freed; dry writes and deletes nothing. A log already cut is left alone.
//
// The usage of every file that goes is added up first, the kept file is then replaced through a
// temporary file and a rename, and only then are the others deleted: a crash between the last two
// leaves their turns counted twice, never lost.
//
// Two trims (the daemon's gc at start and a gc by hand) must not meet in the same directory: one
// would read a file the other has just deleted. They run one at a time.
func TrimLogs(dir, key, run string, dry bool) (int64, error) {
	trimMu.Lock()
	defer trimMu.Unlock()
	d := filepath.Join(dir, "logs", key)
	ents, err := os.ReadDir(d)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	type file struct {
		name string
		size int64
		mod  time.Time
	}
	var logs []file
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || !e.Type().IsRegular() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		logs = append(logs, file{e.Name(), info.Size(), info.ModTime()})
	}
	keep := -1
	for i, l := range logs {
		if l.name == run+".jsonl" {
			keep = i
			break
		}
		if keep < 0 || l.mod.After(logs[keep].mod) {
			keep = i
		}
	}
	if keep < 0 {
		return 0, nil
	}
	var freed int64
	count := func(name string) {
		if info, err := os.Stat(filepath.Join(d, name)); err == nil {
			freed += info.Size()
		}
	}
	var olds []string
	oldTurns := 0
	for i, l := range logs {
		if i == keep {
			continue
		}
		f, err := os.Open(filepath.Join(d, l.name))
		if err != nil {
			return 0, err
		}
		t, _, _, err := usageOf(f)
		f.Close()
		if err != nil {
			return 0, err
		}
		oldTurns += t
		olds = append(olds, l.name)
	}
	kept := filepath.Join(d, logs[keep].name)
	k, err := cutOf(kept)
	if err != nil {
		return 0, err
	}
	if k.rewrite || len(olds) > 0 {
		head := trimmed{Type: trimmedType, Turns: oldTurns}
		if k.start > 0 {
			f, err := os.Open(kept)
			if err != nil {
				return 0, err
			}
			t, c, ok, err := usageOf(io.NewSectionReader(f, 0, k.start))
			f.Close()
			if err != nil {
				return 0, err
			}
			head.Turns += t
			if ok {
				head.Ctx = &c
			}
		}
		var rec []byte
		if head.Turns > 0 || head.Ctx != nil {
			rec, _ = json.Marshal(head)
			rec = append(rec, '\n')
		}
		freed += k.size - int64(len(rec)+len(k.body))
		if !dry {
			if err := writeAtomic(kept, rec, k.body); err != nil {
				return 0, err
			}
		}
	}
	for _, name := range olds {
		count(name)
		count(strings.TrimSuffix(name, ".jsonl") + ".stderr")
		if !dry {
			os.Remove(filepath.Join(d, name))
			os.Remove(filepath.Join(d, strings.TrimSuffix(name, ".jsonl")+".stderr"))
		}
	}
	n, err := trimStderrFile(filepath.Join(d, strings.TrimSuffix(logs[keep].name, ".jsonl")+".stderr"), dry)
	return freed + n, err
}

// keptLog is what stays of a log of size bytes: its records from byte start, in body.
type keptLog struct {
	start, size int64
	body        []byte
	rewrite     bool // something goes, or the head is a trimmed record the new one replaces
}

// cutOf reads the end of the log at path (at most trimBytes) and finds where its last trimRecords
// records start. A log whose head is a trimmed record followed by no more than trimRecords
// records is already cut: rewrite is false.
func cutOf(path string) (keptLog, error) {
	f, err := os.Open(path)
	if err != nil {
		return keptLog{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return keptLog{}, err
	}
	c := keptLog{size: fi.Size()}
	n := min(c.size, trimBytes)
	off := c.size - n
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return keptLog{}, err
	}
	lo := 0
	if off > 0 { // the window began inside a record: it starts at the next line
		var prev [1]byte
		if _, err := f.ReadAt(prev[:], off-1); err != nil {
			return keptLog{}, err
		}
		if prev[0] != '\n' {
			lo = len(buf)
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				lo = i + 1
			}
		}
	}
	w := buf[lo:]
	end := len(w)
	if end > 0 && w[end-1] == '\n' {
		end--
	}
	idx, seen := 0, 0
	for i := end - 1; i >= 0; i-- {
		if w[i] == '\n' {
			if seen++; seen == trimRecords {
				idx = i + 1
				break
			}
		}
	}
	c.start, c.body = off+int64(lo+idx), w[idx:]
	c.rewrite = c.start > 0
	if c.start > 0 {
		first, _ := bufio.NewReader(io.NewSectionReader(f, 0, c.size)).ReadBytes('\n')
		var h trimmed
		if json.Unmarshal(first, &h) == nil && h.Type == trimmedType && c.start == int64(len(first)) {
			c.rewrite = false
		}
	}
	return c, nil
}

// trimStderrFile keeps the last trimStderr bytes of the file at path, from a line start.
func trimStderrFile(path string, dry bool) (int64, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && fi.Size() <= trimStderr {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, trimStderr+1) // the byte before the window says whether it starts a line
	_, err = f.ReadAt(buf, fi.Size()-trimStderr-1)
	f.Close() // before writeAtomic: Windows refuses to rename over an open file
	if err != nil && err != io.EOF {
		return 0, err
	}
	before, buf := buf[0], buf[1:]
	if i := bytes.IndexByte(buf, '\n'); before != '\n' && i >= 0 {
		buf = buf[i+1:]
	}
	if !dry {
		if err := writeAtomic(path, buf); err != nil {
			return 0, err
		}
	}
	return fi.Size() - int64(len(buf)), nil
}

// writeAtomic replaces path with parts through a temporary file in the same directory.
func writeAtomic(path string, parts ...[]byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trim-*.tmp")
	if err != nil {
		return err
	}
	for _, p := range parts {
		if _, err = tmp.Write(p); err != nil {
			break
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// lastRecords is the last n lines of the log at path (all when n <= 0), read back from the end
// in blocks, not whole: a run log can be tens of MB.
func lastRecords(path string, n int) ([]json.RawMessage, error) {
	if n <= 0 {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return splitRecords(b, 0), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var buf []byte
	for pos, newlines := fi.Size(), 0; pos > 0 && newlines <= n; {
		step := min(pos, 64<<10)
		pos -= step
		block := make([]byte, step)
		if _, err := f.ReadAt(block, pos); err != nil && err != io.EOF {
			return nil, err
		}
		newlines += bytes.Count(block, []byte("\n"))
		buf = append(block, buf...)
	}
	return splitRecords(buf, n), nil
}

// splitRecords splits b into lines and keeps the last n (all when n <= 0).
func splitRecords(b []byte, n int) []json.RawMessage {
	if len(b) == 0 {
		return []json.RawMessage{}
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]json.RawMessage, len(lines))
	for i, l := range lines {
		out[i] = json.RawMessage(l)
	}
	return out
}
