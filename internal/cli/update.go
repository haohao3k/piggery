package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sting8k/piggery/internal/proto"
	"github.com/sting8k/piggery/internal/server"
)

// Version is this build's release tag, stamped at build time:
//
//	go build -ldflags "-X github.com/sting8k/piggery/internal/cli.Version=v0.3.0" ./cmd/piggery
//
// "dev" is a build from source with no tag ("dev-<sha>": a local deploy); see server.DevBuild.
var Version = "dev"

// latestRelease is the GitHub API URL of the newest release (the release workflow names its
// assets: piggery-<os>-<arch>, with .exe on Windows, and checksums.txt).
const latestRelease = "https://api.github.com/repos/haohao3k/piggery/releases/latest"

// maxDownload bounds a downloaded binary or checksums file.
const maxDownload = 256 << 20

// updater replaces this binary with the latest release's. Its fields are what tests replace.
type updater struct {
	api          string // latest release JSON
	exe          string // the running binary ("" = os.Executable)
	goos, goarch string
	client       *http.Client
}

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// assetName is the binary of a platform in a release: piggery-<os>-<arch>, with .exe on Windows
// and no version, so releases/latest/download/<name> is a stable install URL.
func assetName(goos, goarch string) string {
	name := fmt.Sprintf("piggery-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// asset is the release asset called name.
func (r release) asset(name string) (string, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, true
		}
	}
	return "", false
}

func (u updater) latest(ctx context.Context) (release, error) {
	var r release
	b, err := u.get(ctx, u.api)
	if err != nil {
		return r, fmt.Errorf("latest release: %w", err)
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Tag == "" {
		return r, fmt.Errorf("latest release: unexpected answer from %s", u.api)
	}
	return r, nil
}

// latestTag is server.Config.Latest for the release at api: the daemon's daily check makes the call
// `update --check` makes.
func latestTag(api string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		r, err := updater{api: api, client: http.DefaultClient}.latest(ctx)
		return r.Tag, err
	}
}

// DaemonLatest is server.Config.Latest of the real binary.
var DaemonLatest = latestTag(latestRelease)

func (u updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "piggery/"+Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && url == u.api {
		return nil, fmt.Errorf("no release published at %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// install downloads r's binary for this platform, checks its sha256 against checksums.txt and
// replaces the running binary (a temp file in its directory, then putBinary). A bad checksum or
// any failure leaves the binary as it was.
func (u updater) install(ctx context.Context, r release) error {
	name := assetName(u.goos, u.goarch)
	binURL, ok := r.asset(name)
	if !ok {
		return fmt.Errorf("release %s has no %s (no build for %s/%s)", r.Tag, name, u.goos, u.goarch)
	}
	sumURL, ok := r.asset("checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt: not installing an unchecked binary", r.Tag)
	}
	sums, err := u.get(ctx, sumURL)
	if err != nil {
		return err
	}
	want := ""
	for _, l := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt of %s does not list %s", r.Tag, name)
	}
	bin, err := u.get(ctx, binURL)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(bin); hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s: sha256 %s does not match checksums.txt (%s); nothing replaced", name, hex.EncodeToString(sum[:]), want)
	}

	exe, err := u.exePath()
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o755)
	if st, err := os.Stat(exe); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".piggery-update-*")
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot write %s (permission denied): run update as a user who can, or reinstall", filepath.Dir(exe))
	}
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // gone after the rename; cleans up on failure
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return putBinary(tmp.Name(), exe, u.goos)
}

// exePath is the file `update` replaces: the running binary (or u.exe), not a symlink to it. On
// Windows `pig.exe` is a hard link to piggery.exe, which EvalSymlinks does not follow: run as pig.exe,
// the file to replace is the piggery.exe beside it, so that pig.exe is not turned into a separate copy.
func (u updater) exePath() (string, error) {
	exe := u.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
	}
	exe, err := filepath.EvalSymlinks(exe)
	if err != nil || u.goos != "windows" {
		return exe, err
	}
	if main := filepath.Join(filepath.Dir(exe), "piggery.exe"); !strings.EqualFold(exe, main) && sameFile(exe, main) {
		return main, nil
	}
	return exe, nil
}

// sameFile reports whether a and b are one file (a hard link to the other).
func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && os.SameFile(sa, sb)
}

// putBinary moves the new binary tmp to exe. A rename replaces a binary atomically, running or not,
// everywhere but on Windows: a running program can be renamed there, not replaced or deleted. So
// there the old binary steps aside first, as exe+".old" (in place of the one an earlier update
// left), and stays until the next update or until someone deletes it. A pig.exe beside it that is a
// hard link to it (the alias the installer makes) would stay a link to the old file: it steps aside
// the same way, as pig.exe.old, and is linked to the new one. Any other pig.exe is not ours.
func putBinary(tmp, exe, goos string) error {
	if goos != "windows" {
		return os.Rename(tmp, exe)
	}
	pig := filepath.Join(filepath.Dir(exe), "pig.exe")
	ours := !strings.EqualFold(pig, exe) && sameFile(pig, exe)
	old := exe + ".old"
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s, which an earlier update left (a daemon of that version may still run: piggery shutdown): %w", old, err)
	}
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe) // back where it was: nothing replaced
		return err
	}
	if ours {
		if err := os.Remove(pig + ".old"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s is updated, but pig.exe still is the old one: cannot remove %s: %w", exe, pig+".old", err)
		}
		if err := os.Rename(pig, pig+".old"); err != nil {
			return fmt.Errorf("%s is updated, but pig.exe still is the old one: %w", exe, err)
		}
		if err := os.Link(exe, pig); err != nil {
			return fmt.Errorf("%s is updated, but pig.exe is gone: link it again (New-Item -ItemType HardLink -Path pig.exe -Target piggery.exe): %w", exe, err)
		}
	}
	return nil
}

// run is `update`: --check prints the versions only; else it installs the latest release unless
// it is this one or this is a dev build (--force for both). It reports whether it replaced the
// binary.
func (u updater) run(ctx context.Context, w io.Writer, check, force bool) (bool, error) {
	if server.DevBuild(Version) && !check && !force {
		return false, errors.New("this is a dev build (not a release); --force replaces it with the latest release")
	}
	r, err := u.latest(ctx)
	if err != nil {
		return false, err
	}
	if check {
		fmt.Fprintf(w, "current %s, latest %s\n", Version, r.Tag)
		if !server.DevBuild(Version) && server.Newer(r.Tag, Version) {
			fmt.Fprintln(w, proto.UpdateNotice(r.Tag))
		}
		return false, nil
	}
	switch {
	case Version == r.Tag && !force:
		fmt.Fprintf(w, "already %s\n", r.Tag)
		return false, nil
	}
	if err := u.install(ctx, r); err != nil {
		return false, err
	}
	fmt.Fprintf(w, "updated %s → %s\n", Version, r.Tag)
	return true, nil
}

// update is `piggery update [--check] [--force] [--replace <names>]`, the one command of an upgrade.
// A running daemon is shut down after the binary is replaced; the next command starts the new one.
// Then the new binary runs `setup --outdated` (this one cannot: it is the old code), with the same
// terminal, so the list of changed templates can be answered; when this binary is already the
// latest, it runs it itself. --check only looks at the versions.
func (e *env) update(args []string) error {
	fs := e.flags("update")
	check := fs.Bool("check", false, "print the current and latest versions only")
	force := fs.Bool("force", false, "install even over a dev build or the same version")
	replace := fs.String("replace", "", "replace the built-in templates and shared prompts you changed, without asking: <name,…|all|none>")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("%w: update takes no arguments", errUsage)
	}
	replaceSet := false
	fs.Visit(func(f *flag.Flag) { replaceSet = replaceSet || f.Name == "replace" })
	if replaceSet && *check {
		return fmt.Errorf("%w: --replace and --check do not go together (--check only looks)", errUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	u := updater{api: latestRelease, goos: runtime.GOOS, goarch: runtime.GOARCH, client: http.DefaultClient}
	if e.upd != nil {
		u = *e.upd
	}
	replaced, err := u.run(ctx, e.stdout, *check, *force)
	if err != nil || *check {
		return err
	}
	setup := []string{"--outdated"}
	if replaceSet {
		setup = append(setup, "--replace="+*replace)
	}
	if !replaced {
		return e.setup(setup) // already the latest: the same second step, by this binary
	}
	if c, err := Dial(e.dir, false); err == nil { // a daemon runs the old binary: stop it
		c.Close()
		if _, err := os.Stat(server.AdminTokenPath(e.dir)); err == nil {
			e.admin = true
			if err := e.shutdown(nil); err != nil {
				return err
			}
		}
	}
	exe, err := u.exePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, append([]string{"setup"}, setup...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the new piggery's `setup --outdated` failed (%w); run it again by hand", err)
	}
	return nil
}
