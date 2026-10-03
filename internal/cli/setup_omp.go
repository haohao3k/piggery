package cli

import (
	"fmt"
	"os"

	"github.com/sting8k/piggery/internal/driver/local"
)

// omp: piggery's extension is the copy the binary carries, unpacked into omp's
// <agent dir>/extensions/piggery (omp loads it by itself; its manifest names the one entry). There
// is no checkout way (pi's --ext): a checkout is `omp -e extensions/omp/index.ts`. The omp worker
// profile (~/.piggery/harness/omp.json) is written by `piggery setup` and the daemon; workers load
// their own copy (local.EnsureOmpWorkerExt), never this one.

// ompHarness: omp's extension talks to the daemon itself (no hooks, no piggery mcp).
var ompHarness = harnessProfile{
	setupTarget: setupTarget{name: "omp", cmd: "omp",
		install: func(o setupOpts) (string, error) { return installOmp(o.dir) },
		refresh: func(o setupOpts) (string, error) { return refreshOmp(o) },
		remove:  func(o setupOpts) (string, error) { return removeOmp(o.dir) },
		status:  func(o setupOpts) harnessState { return ompStatus(o.dir, o.self) },
	},
	profilePath: local.OmpProfilePath,
}

// refreshOmp only updates the managed extension that omp already loads from its agent directory.
// There is no setup-owned registration command to rerun, so a missing managed copy is skipped.
func refreshOmp(o setupOpts) (string, error) {
	dst := local.OmpExtDir(o.dir)
	if _, managed := local.OmpExtVersion(dst); !managed {
		return "omp: skipped (the managed extension is not installed)", nil
	}
	wrote, err := local.InstallOmpExt(dst)
	if err != nil {
		return "", err
	}
	if !wrote {
		return "omp: already current", nil
	}
	return fmt.Sprintf("omp: refreshed piggery's extension (v%d) in %s", local.IntegrationVersion("omp"), dst), nil
}

func installOmp(dir string) (string, error) {
	dst := local.OmpExtDir(dir)
	wrote, err := local.InstallOmpExt(dst)
	if err != nil {
		return "", err
	}
	if !wrote {
		return "omp: piggery's extension is already installed", nil
	}
	return fmt.Sprintf("omp: installed piggery's extension (v%d) in %s\nomp: sessions started from now on join piggery; restart any that are open.", local.IntegrationVersion("omp"), dst), nil
}

func removeOmp(dir string) (string, error) {
	ext := local.OmpExtDir(dir)
	if removed, err := local.RemoveOmpExt(ext); err != nil {
		return "", err
	} else if !removed {
		return "omp: piggery is not installed", nil
	}
	return "omp: removed " + ext, nil
}

// ompStatus: the installed copy, and `piggery` on PATH being this binary (the extension starts the
// daemon with it). A directory there that is not piggery's is reported.
func ompStatus(dir, self string) harnessState {
	st := harnessState{Name: "omp"}
	ext := local.OmpExtDir(dir)
	have, managed := local.OmpExtVersion(ext)
	if !managed {
		if _, err := os.Lstat(ext); err == nil {
			st.Problems = append(st.Problems, problem{ext + " is not piggery's (no \"managed by piggery\" line): setup omp will not replace it", "move it away, then `piggery setup omp`"})
		}
		return st
	}
	st.Installed, st.Detail = true, fmt.Sprintf("%s (v%d)", ext, have)
	st.Problems = append(st.Problems, piggeryOnPath(self)...)
	return st
}
