package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const codexReviewSkill = "piggery-three-review"

// Keep the receipt outside the skill directory so deliberately deleting the skill cannot
// silently turn a refresh into a first install. CODEX_HOME scopes both files and test homes.
func codexSkillPath(home string) string {
	return filepath.Join(home, "skills", codexReviewSkill, "SKILL.md")
}

func codexSkillReceiptPath(home string) string {
	return filepath.Join(home, "piggery-three-review.json")
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
func checkCodexSkillPaths(home string) error {
	for _, p := range []string{filepath.Join(home, "skills"), filepath.Dir(codexSkillPath(home))} {
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
	for _, p := range []string{codexSkillPath(home), codexSkillReceiptPath(home)} {
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
	var state codexSkillState
	if err := checkCodexSkillPaths(home); err != nil {
		return state, err
	}
	raw, err := readOptional(codexSkillReceiptPath(home))
	if err != nil {
		return state, err
	}
	state.content, err = readOptional(codexSkillPath(home))
	if err != nil {
		return state, err
	}
	if raw != nil {
		var record codexSkillReceipt
		if err := json.Unmarshal(raw, &record); err != nil || len(record.SHA256) != 64 {
			return state, fmt.Errorf("preserved %s: invalid Piggery skill receipt", codexSkillReceiptPath(home))
		}
		if state.content == nil || skillHash(state.content) != record.SHA256 {
			return state, fmt.Errorf("preserved %s: managed skill was changed or removed; restore its recorded copy or move the custom skill and receipt aside before setup", codexSkillPath(home))
		}
		state.receipt = &record
		return state, nil
	}
	if _, err := os.Lstat(filepath.Dir(codexSkillPath(home))); err == nil {
		return state, fmt.Errorf("preserved %s: skill directory is not owned by Piggery", filepath.Dir(codexSkillPath(home)))
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
	// Recheck ownership after the host registration work, before touching skill bytes.
	if _, err := inspectCodexSkill(home); err != nil {
		return err
	}
	content := []byte(threeReviewSkill(self, codexReviewSkill))
	record := codexSkillReceipt{SHA256: skillHash(content), TemplateSHA256: skillHash([]byte(threeReviewSkillMD)),
		Executable: self, CreatedSkillsDir: before.newRoot}
	if before.receipt != nil {
		record.CreatedSkillsDir = before.receipt.CreatedSkillsDir
	}
	if err := writeSkillFileAtomic(codexSkillPath(home), content); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeSkillFileAtomic(codexSkillReceiptPath(home), append(raw, '\n'))
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
	state, err := inspectCodexSkill(home)
	if err != nil {
		// Other Piggery host registrations can still be removed while preserving custom bytes.
		return err.Error(), nil
	}
	if state.receipt == nil {
		return "", nil
	}
	if err := os.Remove(codexSkillPath(home)); err != nil {
		return "", err
	}
	if err := os.Remove(codexSkillReceiptPath(home)); err != nil {
		return "", err
	}
	// Remove only empty directories; never recursively remove user additions.
	_ = os.Remove(filepath.Dir(codexSkillPath(home)))
	if state.receipt.CreatedSkillsDir {
		_ = os.Remove(filepath.Join(home, "skills"))
	}
	return "removed Piggery's three-review skill", nil
}
