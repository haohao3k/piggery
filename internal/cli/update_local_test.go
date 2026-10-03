package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type localUpdateFixture struct {
	root       string
	install    string
	binary     string
	receipt    string
	invocation string
}

func newLocalUpdateFixture(t *testing.T, state string) localUpdateFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "checkout with spaces")
	install := filepath.Join(base, "install dir with spaces")
	script := filepath.Join(root, "scripts", "local-dev.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	invocation := filepath.Join(install, "invocation.log")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$1\" > \"$PIGGERY_INSTALL_DIR/invocation.log\"\nprintf 'install=%s\\n' \"$PIGGERY_INSTALL_DIR\" >> \"$PIGGERY_INSTALL_DIR/invocation.log\"\nprintf 'caller=%s\\n' \"$3\" >> \"$PIGGERY_INSTALL_DIR/invocation.log\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(install, "piggery")
	binaryBytes := []byte("managed local piggery")
	if err := os.WriteFile(binary, binaryBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binaryBytes)
	receipt := filepath.Join(install, "piggery.local.json")
	value := map[string]any{
		"schema":        localReceiptSchema,
		"build_mode":    "local",
		"source_root":   root,
		"state":         state,
		"binary_sha256": hex.EncodeToString(sum[:]),
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, b, 0o600); err != nil {
		t.Fatal(err)
	}
	old := LocalSourceRootBase64
	LocalSourceRootBase64 = base64.StdEncoding.EncodeToString([]byte(root))
	t.Cleanup(func() { LocalSourceRootBase64 = old })
	resolvedInstall, err := filepath.EvalSymlinks(install)
	if err != nil {
		t.Fatal(err)
	}
	return localUpdateFixture{root: root, install: resolvedInstall, binary: binary, receipt: receipt, invocation: invocation}
}

func TestLocalUpdatePassesCallerAndOverridesInstallDir(t *testing.T) {
	oldMode := BuildMode
	BuildMode = "local"
	t.Cleanup(func() { BuildMode = oldMode })
	f := newLocalUpdateFixture(t, "failed")
	oldInstall := os.Getenv("PIGGERY_INSTALL_DIR")
	if err := os.Setenv("PIGGERY_INSTALL_DIR", filepath.Join(t.TempDir(), "wrong")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("PIGGERY_INSTALL_DIR", oldInstall) })
	var out strings.Builder
	u := updater{exe: f.binary, caller: "01caller"}
	if replaced, err := u.run(context.Background(), &out, false, true); replaced || err != nil {
		t.Fatalf("local apply = %v, %v; output=%q", replaced, err, out.String())
	}
	b, err := os.ReadFile(f.invocation)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "apply" || lines[1] != "install="+f.install || lines[2] != "caller=01caller" {
		t.Fatalf("script invocation = %q; want apply, install=%s, caller=01caller", b, f.install)
	}
}

func TestLocalUpdateCheckRejectsCaller(t *testing.T) {
	oldMode := BuildMode
	BuildMode = "local"
	t.Cleanup(func() { BuildMode = oldMode })
	f := newLocalUpdateFixture(t, "activated")
	u := updater{exe: f.binary, caller: "solo"}
	if replaced, err := u.run(context.Background(), io.Discard, true, false); replaced || err == nil || !strings.Contains(err.Error(), "--caller cannot be combined with --check") {
		t.Fatalf("check with caller = %v, %v; want usage error", replaced, err)
	}
	if _, err := os.Stat(f.invocation); !os.IsNotExist(err) {
		t.Fatalf("check with caller invoked script: %v", err)
	}
}

func TestLocalUpdateRejectsForeignReceiptAndHashDrift(t *testing.T) {
	oldMode := BuildMode
	BuildMode = "local"
	t.Cleanup(func() { BuildMode = oldMode })
	f := newLocalUpdateFixture(t, "pending")
	b, err := os.ReadFile(f.receipt)
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["source_root"] = filepath.Join(t.TempDir(), "other")
	b, _ = json.Marshal(receipt)
	if err := os.WriteFile(f.receipt, b, 0o600); err != nil {
		t.Fatal(err)
	}
	u := updater{exe: f.binary}
	if replaced, err := u.run(context.Background(), io.Discard, true, false); replaced || err == nil || !strings.Contains(err.Error(), "receipt points to") {
		t.Fatalf("foreign receipt = %v, %v", replaced, err)
	}

	receipt["source_root"] = f.root
	if err := os.WriteFile(f.receipt, mustJSON(t, receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.binary, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if replaced, err := u.run(context.Background(), io.Discard, true, false); replaced || err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("hash drift = %v, %v", replaced, err)
	}
	if _, err := os.Stat(f.invocation); !os.IsNotExist(err) {
		t.Fatalf("invalid receipt invoked script: %v", err)
	}
}

func TestLocalUpdateRejectsMissingStampedCheckout(t *testing.T) {
	oldMode, oldRoot := BuildMode, LocalSourceRootBase64
	BuildMode, LocalSourceRootBase64 = "local", base64.StdEncoding.EncodeToString([]byte(filepath.Join(t.TempDir(), "gone")))
	t.Cleanup(func() { BuildMode, LocalSourceRootBase64 = oldMode, oldRoot })
	f := newLocalUpdateFixture(t, "activated")
	// The fixture changes the stamp, so the installed receipt is deliberately no
	// longer bound to the build's owning checkout.
	LocalSourceRootBase64 = base64.StdEncoding.EncodeToString([]byte(filepath.Join(t.TempDir(), "gone")))
	u := updater{exe: f.binary}
	if replaced, err := u.run(context.Background(), io.Discard, true, false); replaced || err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing checkout = %v, %v", replaced, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
