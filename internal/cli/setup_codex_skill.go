package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const codexReviewSkill = "piggery-dual-lens"

// Keep the receipt outside the skill directory so deliberately deleting the skill cannot
// silently turn a refresh into a first install. CODEX_HOME scopes both files and test homes.
func codexSkillPath(home string) string {
	return reviewSkillPath(home, codexReviewSkill)
}

func codexSkillReceiptPath(home string) string {
	return reviewSkillReceiptPath(home, codexReviewSkill)
}

type codexSkillReceipt struct {
	SHA256           string `json:"sha256"`
	TemplateSHA256   string `json:"template_sha256"`
	Executable       string `json:"executable"`
	CreatedSkillsDir bool   `json:"created_skills_dir"`
}

type codexSkillState struct {
	content []byte
	receipt *codexSkillReceipt
	newRoot bool
}

func skillHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// checkCodexSkillPaths refuses links and special files before any reads or writes. User
// symlinked skill roots are valid host configuration, but not a tree this installer owns.
func checkCodexSkillPaths(home string) error { return checkReviewSkillPaths(home, codexReviewSkill) }

func checkReviewSkillPaths(home, name string) error {
	for _, p := range []string{filepath.Join(home, "skills"), filepath.Dir(reviewSkillPath(home, name))} {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return fmt.Errorf("preserved %s: Piggery requires a regular skill directory", p)
		}
	}
	for _, p := range []string{reviewSkillPath(home, name), reviewSkillReceiptPath(home, name)} {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("preserved %s: Piggery requires a regular skill file", p)
		}
	}
	return nil
}

func inspectCodexSkill(home string) (codexSkillState, error) {
	return inspectCodexSkillForSetup(home, false)
}

func inspectCodexSkillForSetup(home string, restoreMissing bool) (codexSkillState, error) {
	return inspectReviewSkillForSetup(home, codexReviewSkill, restoreMissing)
}

func inspectReviewSkillForSetup(home, name string, restoreMissing bool) (codexSkillState, error) {
	var state codexSkillState
	if err := checkReviewSkillPaths(home, name); err != nil {
		return state, err
	}
	raw, err := readOptional(reviewSkillReceiptPath(home, name))
	if err != nil {
		return state, err
	}
	state.content, err = readOptional(reviewSkillPath(home, name))
	if err != nil {
		return state, err
	}
	if raw != nil {
		var record codexSkillReceipt
		if err := json.Unmarshal(raw, &record); err != nil || len(record.SHA256) != 64 {
			return state, fmt.Errorf("preserved %s: invalid Piggery skill receipt", reviewSkillReceiptPath(home, name))
		}
		if (state.content == nil && !restoreMissing) || (state.content != nil && skillHash(state.content) != record.SHA256) {
			return state, fmt.Errorf("preserved %s: managed skill was changed or removed; restore its recorded copy or move the custom skill and receipt aside before setup", reviewSkillPath(home, name))
		}
		state.receipt = &record
		return state, nil
	}
	if _, err := os.Lstat(filepath.Dir(reviewSkillPath(home, name))); err == nil {
		return state, fmt.Errorf("preserved %s: skill directory is not owned by Piggery", filepath.Dir(reviewSkillPath(home, name)))
	} else if !os.IsNotExist(err) {
		return state, err
	}
	_, err = os.Lstat(filepath.Join(home, "skills"))
	state.newRoot = os.IsNotExist(err)
	if err != nil && !os.IsNotExist(err) {
		return state, err
	}
	return state, nil
}

func writeCodexSkill(home, self string, before codexSkillState) error {
	return writeReviewSkill(home, self, codexReviewSkill, before)
}

func writeReviewSkill(home, self, name string, before codexSkillState) error {
	// Recheck ownership after the host registration work, before touching skill bytes.
	if _, err := inspectReviewSkillForSetup(home, name, before.receipt != nil && before.content == nil); err != nil {
		return err
	}
	content := []byte(dualLensSkill(self, name))
	record := codexSkillReceipt{SHA256: skillHash(content), TemplateSHA256: skillHash([]byte(dualLensSkillMD)),
		Executable: self, CreatedSkillsDir: before.newRoot}
	if before.receipt != nil {
		record.CreatedSkillsDir = before.receipt.CreatedSkillsDir
	}
	if err := writeSkillFileAtomic(reviewSkillPath(home, name), content); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeSkillFileAtomic(reviewSkillReceiptPath(home, name), append(raw, '\n'))
}

// Unlike the older hook writer, use an exclusive random temporary file so a pre-existing
// .piggery-tmp symlink cannot redirect this new install surface.
func writeSkillFileAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".piggery-skill-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func removeCodexSkill(home string) (string, error) {
	var notes []string
	for _, name := range []string{codexReviewSkill, legacyDualLaneSkill, legacyReviewSkill} {
		note, err := removeReviewSkill(home, name)
		if err != nil {
			return strings.Join(notes, "\n"), err
		}
		if note != "" {
			notes = append(notes, note)
		}
	}
	return strings.Join(notes, "\n"), nil
}

func removeReviewSkill(home, name string) (string, error) {
	state, err := inspectReviewSkillForSetup(home, name, false)
	if err != nil {
		// Other Piggery host registrations can still be removed while preserving custom bytes.
		return err.Error(), nil
	}
	if state.receipt == nil {
		return "", nil
	}
	if err := os.Remove(reviewSkillPath(home, name)); err != nil {
		return "", err
	}
	if err := os.Remove(reviewSkillReceiptPath(home, name)); err != nil {
		return "", err
	}
	// Remove only empty directories; never recursively remove user additions.
	_ = os.Remove(filepath.Dir(reviewSkillPath(home, name)))
	if state.receipt.CreatedSkillsDir {
		_ = os.Remove(filepath.Join(home, "skills"))
	}
	return "removed Piggery skill " + name, nil
}

// installCodexReviewShortcut has no dependency on Codex's hooks, MCP registration or CLI.
// Only the explicit addon setup may recreate a deliberately removed managed shortcut.
func installCodexReviewShortcut(home, self string, restoreMissing bool) (string, error) {
	state, err := inspectCodexSkillForSetup(home, restoreMissing)
	if err != nil {
		return "", err
	}
	if codexReviewSkillCurrent(state, self) {
		return finishReviewShortcutMigration(home, self, "dual-lens (Codex): ready"), nil
	}
	if err := writeCodexSkill(home, self, state); err != nil {
		return "", err
	}
	return finishReviewShortcutMigration(home, self, fmt.Sprintf("dual-lens (Codex): installed $piggery-dual-lens in %s", codexSkillPath(home))), nil
}

func codexReviewSkillCurrent(state codexSkillState, self string) bool {
	return state.receipt != nil && bytes.Equal(state.content, []byte(dualLensSkill(self, codexReviewSkill))) &&
		state.receipt.TemplateSHA256 == skillHash([]byte(dualLensSkillMD)) && state.receipt.Executable == self
}

func codexReviewStatus(home, self string) string {
	state, err := inspectCodexSkill(home)
	if err != nil {
		return "dual-lens (Codex): needs attention: " + err.Error() + "; use piggery setup dual-lens"
	}
	if codexReviewSkillCurrent(state, self) {
		return "dual-lens (Codex): ready"
	}
	return "dual-lens (Codex): shortcut available; install or update with piggery setup dual-lens"
}

// The addon ships with the fork, but a custom or deleted shortcut cannot block adapter upkeep.
func codexSetupWithReview(home, self, adapterMessage string, includeMissing bool) string {
	// An unchanged installed legacy shortcut opts into migration. Deleted/custom copies do not.
	for _, name := range []string{legacyReviewSkill, legacyDualLaneSkill} {
		if legacy, err := inspectReviewSkillForSetup(home, name, false); err == nil && legacy.receipt != nil {
			includeMissing = true
		}
	}
	if state, err := inspectCodexSkill(home); err == nil && state.receipt == nil && !includeMissing {
		return adapterMessage + "\n" + codexReviewStatus(home, self)
	}
	note, err := installCodexReviewShortcut(home, self, false)
	if err != nil {
		note = "warning: dual-lens shortcut not updated: " + err.Error() + "; use piggery setup dual-lens"
	}
	return adapterMessage + "\n" + note
}

const legacyReviewSkill = "piggery-three-review"
const legacyDualLaneSkill = "piggery-dual-lane-adjudication"

func reviewSkillPath(home, name string) string {
	return filepath.Join(home, "skills", name, "SKILL.md")
}
func reviewSkillReceiptPath(home, name string) string {
	return filepath.Join(home, name+".json")
}

// Retire only a receipt-owned unchanged legacy shortcut, after the new one is ready.
func finishReviewShortcutMigration(home, self, message string) string {
	// Keep an installed, unchanged old name as a thin entry into the canonical workflow.
	// Customized/deleted aliases remain untouched, even during explicit canonical setup.
	if old, err := inspectReviewSkillForSetup(home, legacyDualLaneSkill, false); err != nil {
		message += "\nwarning: legacy shortcut preserved: " + err.Error()
	} else if old.receipt != nil {
		if err := writeReviewSkill(home, self, legacyDualLaneSkill, old); err != nil {
			message += "\nwarning: legacy shortcut preserved: " + err.Error()
		}
	}
	note, err := removeReviewSkill(home, legacyReviewSkill)
	if err != nil {
		return message + "\nwarning: legacy shortcut preserved: " + err.Error()
	}
	if note != "" {
		return message + "\n" + note
	}
	return message
}
