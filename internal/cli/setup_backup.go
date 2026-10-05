package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// setupBackupPath is where `setup <harness>` keeps the one backup of the Human's config file path:
// <dir>/backups/setup/<harness>/<file name>.
func setupBackupPath(dir, harness, path string) string {
	return filepath.Join(dir, "backups", "setup", harness, filepath.Base(path))
}

// backupHumanConfig is called right before setup writes the Human's config file at path. A file that
// does not have piggery's part yet (ours says whether it has) is copied to setupBackupPath (0600:
// it may hold keys), over an older backup, so the backup is the state just before piggery came in.
// A file that has piggery's part (a second run, an upgrade) keeps the backup there is. It returns
// the line to tell the Human ("" when it made none). `remove` never calls it and never restores.
func backupHumanConfig(dir, harness, path string, ours func([]byte) bool) (string, error) {
	b, err := readOptional(path)
	if err != nil || b == nil || ours(b) {
		return "", err
	}
	dst := setupBackupPath(dir, harness, path)
	if err := writeFileAtomic(dst, b); err != nil {
		return "", err
	}
	if err := os.Chmod(dst, 0o600); err != nil { // writeFileAtomic keeps the mode of an older backup
		return "", err
	}
	return fmt.Sprintf("%s: kept a copy of %s in %s (setup remove does not restore it)", harness, path, dst), nil
}

// contains is an `ours` for a file that has piggery's part when it has mark.
func contains(mark string) func([]byte) bool {
	return func(b []byte) bool { return bytes.Contains(b, []byte(mark)) }
}

// backupLines is the backup messages that were made (the empty ones dropped), one per line, each
// ending in a newline.
func backupLines(msgs []string) string {
	var b strings.Builder
	for _, m := range msgs {
		if m != "" {
			b.WriteString(m + "\n")
		}
	}
	return b.String()
}
