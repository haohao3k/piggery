package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update against a fake GitHub API: --check writes nothing; a binary whose sha256 is not the
// one in checksums.txt is refused and the running binary stays; the right one replaces it (the
// asset for this os/arch, through a symlink to the binary, keeping its mode).
func TestUpdate(t *testing.T) {
	skipOnWindows(t, "replaces the binary through a symlink and checks its mode bits (TestUpdateOnWindowsMovesTheOldBinaryAside is the Windows path)")
	newBin, otherBin := []byte("piggery v0.2.0 linux arm64"), []byte("piggery v0.2.0 darwin arm64")
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	sums := sum(otherBin) + "  piggery-darwin-arm64\n" + sum(newBin) + "  piggery-linux-arm64\n"
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "assets": []map[string]string{
			{"name": "piggery-darwin-arm64", "browser_download_url": srv.URL + "/darwin"},
			{"name": "piggery-linux-arm64", "browser_download_url": srv.URL + "/linux"},
			{"name": "checksums.txt", "browser_download_url": srv.URL + "/sums"},
		}})
	})
	mux.HandleFunc("/darwin", func(w http.ResponseWriter, _ *http.Request) { w.Write(otherBin) })
	mux.HandleFunc("/linux", func(w http.ResponseWriter, _ *http.Request) { w.Write(newBin) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, sums) })

	dir := t.TempDir()
	exe := filepath.Join(dir, "piggery")
	if err := os.WriteFile(exe, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "piggery")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	old := Version
	Version = "v0.1.0"
	defer func() { Version = old }()
	u := updater{api: srv.URL + "/latest", exe: link, goos: "linux", goarch: "arm64", client: srv.Client()}
	unchanged := func(when string) {
		t.Helper()
		if b, _ := os.ReadFile(exe); string(b) != "old" {
			t.Fatalf("%s: binary is %q; want it unchanged", when, b)
		}
		if ents, _ := os.ReadDir(dir); len(ents) != 1 {
			t.Fatalf("%s: %d files beside the binary; want no temp file left", when, len(ents))
		}
	}

	var out strings.Builder
	if replaced, err := u.run(context.Background(), &out, true, false); err != nil || replaced ||
		out.String() != "current v0.1.0, latest v0.2.0\nv0.2.0 available: piggery update\n" {
		t.Fatalf("--check = %v, %v, %q", replaced, err, out.String())
	}
	unchanged("--check")

	sums = strings.Replace(sums, sum(newBin), sum([]byte("tampered")), 1)
	if replaced, err := u.run(context.Background(), io.Discard, false, false); err == nil || replaced ||
		!strings.Contains(err.Error(), "does not match checksums.txt") {
		t.Fatalf("bad checksum = %v, %v; want refused", replaced, err)
	}
	unchanged("bad checksum")

	sums = strings.Replace(sums, sum([]byte("tampered")), sum(newBin), 1)
	out.Reset()
	if replaced, err := u.run(context.Background(), &out, false, false); err != nil || !replaced || out.String() != "updated v0.1.0 → v0.2.0\n" {
		t.Fatalf("update = %v, %v, %q", replaced, err, out.String())
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newBin) {
		t.Fatalf("binary after update = %q; want the linux/arm64 asset", b)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced instead of the binary it points to")
	}
	if st, _ := os.Stat(exe); st.Mode().Perm() != 0o750 {
		t.Fatalf("mode %v; want the old binary's 0750", st.Mode().Perm())
	}

	// Moving off an old checkout build uses the same verified release install, even with
	// no checkout or receipt. --force retains upstream's explicit dev-build replacement rule.
	if err := os.WriteFile(exe, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	Version = "local-old-installation"
	if replaced, err := u.run(context.Background(), io.Discard, false, false); replaced || err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("development build without --force: %v, %v", replaced, err)
	}
	unchanged("development build without --force")
	if replaced, err := u.run(context.Background(), io.Discard, false, true); err != nil || !replaced {
		t.Fatalf("development build to fork release: %v, %v", replaced, err)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != string(newBin) {
		t.Fatalf("development build was not replaced by the release: %q, %v", b, err)
	}
}

// A development installation checks the same fork release as the daemon, without a checkout
// or receipt; a missing fork release must never trigger an upstream request.
func TestForkReleaseSource(t *testing.T) {
	oldClient, oldVersion := http.DefaultClient, Version
	t.Cleanup(func() { http.DefaultClient, Version = oldClient, oldVersion })
	Version = "local-old-installation"
	requests, status := 0, http.StatusOK
	http.DefaultClient = &http.Client{Transport: releaseRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests++
		if got := req.URL.String(); got != "https://api.github.com/repos/haohao3k/piggery/releases/latest" {
			return nil, fmt.Errorf("unexpected release source: %s", got)
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status),
			Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.7.2"}`)), Header: make(http.Header)}, nil
	})}
	var out strings.Builder
	e := &env{stdout: &out}
	if err := e.update([]string{"--check"}); err != nil || out.String() != "current local-old-installation, latest v0.7.2\n" {
		t.Fatalf("fork release check: %q, %v", out.String(), err)
	}
	if tag, err := DaemonLatest(context.Background()); err != nil || tag != "v0.7.2" {
		t.Fatalf("daemon release check: %q, %v", tag, err)
	}
	status = http.StatusNotFound
	if err := e.update([]string{"--check"}); err == nil || !strings.Contains(err.Error(), "no release published") {
		t.Fatalf("missing fork release: %v", err)
	}
	if requests != 3 {
		t.Fatalf("got %d requests; expected one per check with no fallback", requests)
	}
}

type releaseRoundTrip func(*http.Request) (*http.Response, error)

func (f releaseRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// On Windows the asset is piggery-windows-<arch>.exe, and the binary it replaces (which may be
// running: Windows lets a running program be renamed, not replaced) steps aside as piggery.exe.old,
// where the next update replaces it. goos is the updater's, so every OS runs this.
func TestUpdateOnWindowsMovesTheOldBinaryAside(t *testing.T) {
	bins := map[string][]byte{"v0.2.0": []byte("piggery v0.2.0 windows amd64"), "v0.3.0": []byte("piggery v0.3.0 windows amd64")}
	tag := "v0.2.0"
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": []map[string]string{
			{"name": "piggery-windows-amd64", "browser_download_url": srv.URL + "/noext"},
			{"name": "piggery-windows-amd64.exe", "browser_download_url": srv.URL + "/exe"},
			{"name": "checksums.txt", "browser_download_url": srv.URL + "/sums"},
		}})
	})
	mux.HandleFunc("/exe", func(w http.ResponseWriter, _ *http.Request) { w.Write(bins[tag]) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		s := sha256.Sum256(bins[tag])
		io.WriteString(w, hex.EncodeToString(s[:])+"  piggery-windows-amd64.exe\n")
	})

	dir := t.TempDir()
	exe := filepath.Join(dir, "piggery.exe")
	if err := os.WriteFile(exe, []byte("piggery v0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := Version
	defer func() { Version = old }()
	u := updater{api: srv.URL + "/latest", exe: exe, goos: "windows", goarch: "amd64", client: srv.Client()}
	for _, step := range []struct{ from, to, aside string }{
		{"v0.1.0", "v0.2.0", "piggery v0.1.0"},
		{"v0.2.0", "v0.3.0", "piggery v0.2.0 windows amd64"},
	} {
		Version, tag = step.from, step.to
		if replaced, err := u.run(context.Background(), io.Discard, false, false); err != nil || !replaced {
			t.Fatalf("update to %s = %v, %v", step.to, replaced, err)
		}
		if b, _ := os.ReadFile(exe); string(b) != string(bins[step.to]) {
			t.Fatalf("after the update to %s the binary is %q", step.to, b)
		}
		if b, _ := os.ReadFile(exe + ".old"); string(b) != step.aside {
			t.Fatalf("after the update to %s piggery.exe.old is %q; want the binary it replaced", step.to, b)
		}
		if ents, _ := os.ReadDir(dir); len(ents) != 2 {
			t.Fatalf("after the update to %s: %d files; want the binary and the one moved aside", step.to, len(ents))
		}
	}
}

// pig.exe, the alias the Windows installer makes, is a hard link to piggery.exe. An update must keep it
// one: run as pig.exe it replaces the piggery.exe beside it (not itself, which would leave pig.exe a copy
// and piggery.exe old), and either way pig.exe steps aside as pig.exe.old and is linked to the new binary.
// A pig.exe that is not a link to piggery.exe is someone else's and stays as it is. goos is the updater's,
// so every OS runs this.
func TestUpdateKeepsPigExeLinked(t *testing.T) {
	for _, c := range []struct {
		name, runAs string
		linked      bool
	}{
		{"run as pig.exe", "pig.exe", true},
		{"run as piggery.exe", "piggery.exe", true},
		{"someone else's pig.exe", "piggery.exe", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir()) // exePath resolves links; so must the expectation
			if err != nil {
				t.Fatal(err)
			}
			main, pig := filepath.Join(dir, "piggery.exe"), filepath.Join(dir, "pig.exe")
			if err := os.WriteFile(main, []byte("v1"), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.linked {
				if err := os.Link(main, pig); err != nil {
					t.Skipf("no hard links here: %v", err)
				}
			} else if err := os.WriteFile(pig, []byte("not piggery"), 0o755); err != nil {
				t.Fatal(err)
			}
			u := updater{exe: filepath.Join(dir, c.runAs), goos: "windows"}
			for _, v := range []string{"v2", "v3"} { // the second finds a pig.exe.old to replace
				exe, err := u.exePath()
				if err != nil || exe != main {
					t.Fatalf("the file to replace is %q, %v; want %s", exe, err, main)
				}
				tmp := filepath.Join(dir, ".piggery-update-new")
				if err := os.WriteFile(tmp, []byte(v), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := putBinary(tmp, exe, "windows"); err != nil {
					t.Fatal(err)
				}
				if b, _ := os.ReadFile(main); string(b) != v {
					t.Fatalf("piggery.exe is %q after the update to %s", b, v)
				}
			}
			b, _ := os.ReadFile(pig)
			if !c.linked {
				if string(b) != "not piggery" || fileExists(pig+".old") {
					t.Fatalf("a pig.exe that is not ours was touched: %q", b)
				}
				return
			}
			if string(b) != "v3" || !sameFile(pig, main) {
				t.Fatalf("pig.exe is %q, linked=%v; want the new binary, one file with piggery.exe", b, sameFile(pig, main))
			}
			if old, _ := os.ReadFile(pig + ".old"); string(old) != "v2" {
				t.Fatalf("pig.exe.old is %q; want the pig.exe it replaced (v2)", old)
			}
		})
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
