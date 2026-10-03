package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const localReceiptSchema = 1

// localReceipt is the small binding written by scripts/local-dev.sh next to the
// managed executable. The source root is duplicated in the build stamp and the
// receipt so an installed local binary cannot silently choose a checkout based on
// the caller's current directory.
type localReceipt struct {
	Schema       int    `json:"schema"`
	BuildMode    string `json:"build_mode"`
	SourceRoot   string `json:"source_root"`
	State        string `json:"state"`
	BinarySHA256 string `json:"binary_sha256"`
}

type localInstall struct {
	exe        string
	installDir string
	receipt    string
	root       string
	script     string
}

// localExecutable resolves the file that will be replaced. Using the resolved
// path also makes a symlinked PATH entry use the receipt beside its target,
// which is the only file local-dev can safely update.
func (u updater) localExecutable() (string, error) {
	exe := u.exe
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve current piggery executable: %w", err)
		}
	}
	if !filepath.IsAbs(exe) {
		var err error
		exe, err = filepath.Abs(exe)
		if err != nil {
			return "", fmt.Errorf("resolve current piggery executable %q: %w", exe, err)
		}
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve current piggery executable %q: %w", exe, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve current piggery executable %q: %w", exe, err)
	}
	if filepath.Base(resolved) != "piggery" {
		return "", fmt.Errorf("local update requires the managed piggery executable, got %q; run scripts/local-dev.sh apply in the owning checkout", resolved)
	}
	if _, err := requireRegularFile(resolved, "managed piggery executable"); err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func requireRegularFile(path, label string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q is not a regular file; refusing local update", label, path)
	}
	return info, nil
}

func decodeLocalSourceRoot() (string, error) {
	if strings.TrimSpace(LocalSourceRootBase64) == "" {
		return "", errors.New("local build has no stamped owning checkout; run scripts/local-dev.sh apply in the owning checkout")
	}
	b, err := base64.StdEncoding.DecodeString(LocalSourceRootBase64)
	if err != nil || len(b) == 0 {
		return "", errors.New("local build has an invalid stamped owning checkout; run scripts/local-dev.sh apply in the owning checkout")
	}
	if strings.IndexByte(string(b), 0) >= 0 || !filepath.IsAbs(string(b)) {
		return "", errors.New("local build has an invalid stamped owning checkout; run scripts/local-dev.sh apply in the owning checkout")
	}
	root := filepath.Clean(string(b))
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("owning local checkout %q is unavailable: %w; run scripts/local-dev.sh apply in that checkout", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("stamped owning local checkout %q is not a directory; run scripts/local-dev.sh apply in that checkout", root)
	}
	return root, nil
}

func readLocalReceipt(path string) (localReceipt, error) {
	var receipt localReceipt
	if _, err := requireRegularFile(path, "local installation receipt"); err != nil {
		return receipt, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return receipt, fmt.Errorf("read local installation receipt %q: %w", path, err)
	}
	if err := json.Unmarshal(b, &receipt); err != nil {
		return receipt, fmt.Errorf("read local installation receipt %q: invalid JSON: %w", path, err)
	}
	return receipt, nil
}

func localBinarySHA256(path string) (string, error) {
	if _, err := requireRegularFile(path, "managed piggery executable"); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read managed piggery executable %q: %w", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (u updater) localBinding() (localInstall, error) {
	exe, err := u.localExecutable()
	if err != nil {
		return localInstall{}, err
	}
	root, err := decodeLocalSourceRoot()
	if err != nil {
		return localInstall{}, err
	}
	receiptPath := filepath.Join(filepath.Dir(exe), "piggery.local.json")
	receipt, err := readLocalReceipt(receiptPath)
	if err != nil {
		return localInstall{}, fmt.Errorf("local update is not bound to a managed installation: %w; run scripts/local-dev.sh apply in the owning checkout", err)
	}
	if receipt.Schema != localReceiptSchema || receipt.BuildMode != "local" {
		return localInstall{}, fmt.Errorf("local installation receipt %q is not a schema %d local receipt; run scripts/local-dev.sh apply in the owning checkout", receiptPath, localReceiptSchema)
	}
	if receipt.SourceRoot != root {
		return localInstall{}, fmt.Errorf("local installation receipt points to %q, but this binary is stamped for %q; run scripts/local-dev.sh apply in the owning checkout", receipt.SourceRoot, root)
	}
	if len(receipt.BinarySHA256) != sha256.Size*2 {
		return localInstall{}, fmt.Errorf("local installation receipt %q has no valid binary_sha256; run scripts/local-dev.sh apply in the owning checkout", receiptPath)
	}
	if _, err := hex.DecodeString(receipt.BinarySHA256); err != nil {
		return localInstall{}, fmt.Errorf("local installation receipt %q has an invalid binary_sha256; run scripts/local-dev.sh apply in the owning checkout", receiptPath)
	}
	if receipt.State != "activated" && receipt.State != "pending" && receipt.State != "failed" {
		return localInstall{}, fmt.Errorf("local installation receipt %q has unsupported state %q; run scripts/local-dev.sh apply in the owning checkout", receiptPath, receipt.State)
	}
	got, err := localBinarySHA256(exe)
	if err != nil {
		return localInstall{}, err
	}
	if !strings.EqualFold(got, receipt.BinarySHA256) {
		return localInstall{}, fmt.Errorf("local installation receipt %q does not match the managed executable (sha256 %s, receipt %s); run scripts/local-dev.sh apply in the owning checkout", receiptPath, got, receipt.BinarySHA256)
	}
	script := filepath.Join(root, "scripts", "local-dev.sh")
	if _, err := requireRegularFile(script, "local development script"); err != nil {
		return localInstall{}, fmt.Errorf("owning local checkout is missing scripts/local-dev.sh: %w; run scripts/local-dev.sh apply in that checkout", err)
	}
	if info, err := os.Stat(script); err != nil || info.Mode()&0111 == 0 {
		if err != nil {
			return localInstall{}, fmt.Errorf("inspect local development script %q: %w; run scripts/local-dev.sh apply in that checkout", script, err)
		}
		return localInstall{}, fmt.Errorf("local development script %q is not executable; run chmod +x scripts/local-dev.sh and retry", script)
	}
	return localInstall{exe: exe, installDir: filepath.Dir(exe), receipt: receiptPath, root: root, script: script}, nil
}

func localCommandEnv(installDir string) []string {
	const key = "PIGGERY_INSTALL_DIR="
	env := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, key) {
			env = append(env, value)
		}
	}
	return append(env, key+installDir)
}

// runLocal delegates both local checking and local activation to the exact
// checkout stamped into the executable. The script owns daemon gating,
// replacement, integration refresh and restart; the CLI deliberately does not
// add another shutdown or restart after it returns.
func (u updater) runLocal(ctx context.Context, w io.Writer, check, force bool, caller string) (bool, error) {
	if caller != "" && check {
		return false, fmt.Errorf("%w: --caller cannot be combined with --check", errUsage)
	}
	binding, err := u.localBinding()
	if err != nil {
		return false, err
	}
	mode := "apply"
	if check {
		mode = "check"
	}
	args := []string{binding.script, mode}
	if mode == "apply" && caller != "" {
		args = append(args, "--caller", caller)
	}
	// --force is intentionally a no-op for a local checkout: it may request the
	// same guarded apply, but it can never skip the local-dev idle gate.
	_ = force
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = binding.root
	cmd.Env = localCommandEnv(binding.installDir)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("local piggery %s failed: %w; run scripts/local-dev.sh %s in %s", mode, err, mode, binding.root)
	}
	return false, nil
}
