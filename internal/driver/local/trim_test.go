package local

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// treeOf maps every file under dir to its content (and size), to compare a directory before and
// after.
func treeOf(t *testing.T, dir string) (map[string]string, int64) {
	t.Helper()
	out, total := map[string]string{}, int64(0)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		total += int64(len(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, total
}

// A participant with four runs whose current one (run_id) is neither the newest file nor the last
// by name: a dry run touches no byte and reports what the real one then frees, to the byte; the
// real one keeps the current run's log and no other; cutting again frees nothing.
func TestTrimLogsDryRunTouchesNothingAndFreedIsWhatWentOff(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs", "w1")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	var big strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&big, "{\"type\":\"turn_end\",\"n\":%d}\n", i)
	}
	now := time.Now()
	for i, r := range []string{"r1", "r2", "r3", "r4"} { // r2 is current; r4 is the newest, r3 the last by name but one
		body := "{\"type\":\"turn_end\"}\n"
		if r == "r2" {
			body = big.String()
		}
		for ext, content := range map[string]string{".jsonl": body, ".stderr": strings.Repeat("err\n", 40000)} {
			p := filepath.Join(logs, r+ext)
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, now, now.Add(time.Duration(i-5)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, sizeBefore := treeOf(t, dir)

	dryFreed, err := TrimLogs(dir, "w1", "r2", true)
	if err != nil || dryFreed <= 0 {
		t.Fatalf("dry TrimLogs = %d, %v", dryFreed, err)
	}
	if after, _ := treeOf(t, dir); !sameTree(before, after) {
		t.Fatal("a dry run changed the directory")
	}

	freed, err := TrimLogs(dir, "w1", "r2", false)
	after, sizeAfter := treeOf(t, dir)
	if err != nil || freed != dryFreed || freed != sizeBefore-sizeAfter {
		t.Fatalf("freed %d (dry said %d), the directory lost %d: %v", freed, dryFreed, sizeBefore-sizeAfter, err)
	}
	if len(after) != 2 || after[filepath.Join(logs, "r2.jsonl")] == "" || after[filepath.Join(logs, "r2.stderr")] == "" {
		t.Fatalf("kept %d files; want r2's log and stderr only", len(after))
	}
	if again, err := TrimLogs(dir, "w1", "r2", false); err != nil || again != 0 {
		t.Fatalf("cut again freed %d, %v", again, err)
	}
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// Tail reads a big log from its end: the last n records, whatever the block size, and all of
// them when n is 0 or more than the file holds.
func TestLastRecordsOfABigLog(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "{\"n\":%d,\"pad\":\"%s\"}\n", i, strings.Repeat("x", 90))
	}
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ n, want int }{{2000, 2000}, {0, 5000}, {9000, 5000}} {
		recs, err := lastRecords(path, c.n)
		if err != nil || len(recs) != c.want || !strings.HasPrefix(string(recs[len(recs)-1]), `{"n":4999,`) ||
			!strings.HasPrefix(string(recs[0]), fmt.Sprintf(`{"n":%d,`, 5000-c.want)) {
			t.Fatalf("lastRecords(%d) = %d records, %v", c.n, len(recs), err)
		}
	}
}
