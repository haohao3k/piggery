package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/sting8k/piggery/manifests"
)

// After `piggery update` the first command starts the new daemon, and its start brings the home up to
// date (Unpack, EnsureFiles) before the user runs `piggery setup --outdated`, which then finds nothing
// to do and so nothing to say. The daemon notes what it did in a file in the home; the next setup
// reads it, says it once, and deletes it.
const homeChangesFile = ".home-changes.json"

type homeChanges struct {
	Added, Updated []string // templates, by name
	Filled         []Filled // config keys and shared prompts added
}

// NoteHomeChanges adds what a start of the daemon changed in the home to the note setup will print.
// Nothing changed writes nothing. A failure is not worth failing a start for: the note is a courtesy.
func NoteHomeChanges(dir string, unp manifests.Unpacked, filled []Filled) {
	if len(unp.Added)+len(unp.Updated)+len(filled) == 0 {
		return
	}
	c := readHomeChanges(dir)
	c.Added, c.Updated = mergeNames(c.Added, unp.Added), mergeNames(c.Updated, unp.Updated)
	c.Filled = mergeFilled(c.Filled, filled)
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, homeChangesFile+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, homeChangesFile)) != nil {
		os.Remove(tmp.Name())
	}
}

// TakeHomeChanges is what a setup reports: what the daemon noted since the last one, then what this
// setup's own refresh (unp, filled) just did. It deletes the note.
func TakeHomeChanges(dir string, unp manifests.Unpacked, filled []Filled) (manifests.Unpacked, []Filled) {
	c := readHomeChanges(dir)
	os.Remove(filepath.Join(dir, homeChangesFile))
	return manifests.Unpacked{Added: mergeNames(c.Added, unp.Added), Updated: mergeNames(c.Updated, unp.Updated)}, mergeFilled(c.Filled, filled)
}

func readHomeChanges(dir string) (c homeChanges) {
	b, err := os.ReadFile(filepath.Join(dir, homeChangesFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c
	}
	_ = json.Unmarshal(b, &c) // a note that cannot be read is dropped
	return c
}

// mergeNames is a then the names of b that a lacks, in order.
func mergeNames(a, b []string) []string {
	for _, n := range b {
		if !slices.Contains(a, n) {
			a = append(a, n)
		}
	}
	return a
}

// mergeFilled is a plus b, a file's added keys joined once.
func mergeFilled(a, b []Filled) []Filled {
	for _, f := range b {
		if i := slices.IndexFunc(a, func(x Filled) bool { return x.Path == f.Path && x.Kind == f.Kind }); i >= 0 {
			a[i].Added = mergeNames(a[i].Added, f.Added)
		} else {
			a = append(a, f)
		}
	}
	return a
}
