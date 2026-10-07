package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sting8k/piggery/manifests"
	"gopkg.in/yaml.v3"
)

func TestDualLensSkillPrintsEmbeddedCoordinatorWithoutDaemon(t *testing.T) {
	// Printing a workflow must work before setup, even in a participant shell. It must not
	// create a home, connect to a daemon, or turn a skill lookup into a review run.
	t.Setenv("PIGGERY_ID", "unused-participant")
	t.Setenv("PIGGERY_TOKEN", "unused-token")
	home := filepath.Join(t.TempDir(), "not-created")
	var out, stderr bytes.Buffer
	if code := Main(home, []string{"skills", "dual-lens"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("printing a skill touched the Piggery home: %v", err)
	}
	manifest, err := manifests.Builtin("dual-lens")
	if err != nil {
		t.Fatal(err)
	}
	var team struct {
		Roles map[string]struct{ Instructions string }
	}
	if err := yaml.Unmarshal([]byte(manifest), &team); err != nil {
		t.Fatal(err)
	}
	playbook := team.Roles["lead"].Instructions
	if playbook == "" || !strings.Contains(out.String(), playbook) {
		t.Fatal("CLI skill is not the same playbook as the embedded coordinator role")
	}
}

func TestSkillsTopicRouting(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"default", []string{"skills"}, 0},
		{"unknown", []string{"skills", "unknown"}, 2},
		{"extra", []string{"skills", "dual-lens", "execute"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := Main(t.TempDir(), tc.args, &out, &stderr); code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, stderr.String())
			}
			if tc.name == "default" && out.String() != skillsMD {
				t.Fatal("default agent guide changed")
			}
		})
	}
}

func TestLegacyReviewTopicUsesDualLanePlaybook(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Main(t.TempDir(), []string{"skills", "three-review"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(out.String(), manifests.DualLensInstructions()) {
		t.Fatal("legacy command did not use new playbook")
	}
	manifest, err := manifests.Builtin("triple-review")
	if err != nil {
		t.Fatal(err)
	}
	var team struct {
		Roles  map[string]struct{ Instructions string }
		Limits struct{ Concurrency int }
	}
	if err := yaml.Unmarshal([]byte(manifest), &team); err != nil {
		t.Fatal(err)
	}
	if len(team.Roles) != 3 || team.Limits.Concurrency != 3 || team.Roles["lead"].Instructions != manifests.DualLensInstructions() {
		t.Fatal("legacy template retained old workflow")
	}
}

func TestLegacyDualLensEntriesUseCanonicalContract(t *testing.T) {
	canonical, err := manifests.Builtin("dual-lens")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := yaml.Unmarshal([]byte(canonical), &want); err != nil {
		t.Fatal(err)
	}
	delete(want, "template")
	for _, name := range []string{"dual-lane-adjudication", "triple-review"} {
		alias, err := manifests.Builtin(name)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := yaml.Unmarshal([]byte(alias), &got); err != nil {
			t.Fatal(err)
		}
		delete(got, "template")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs from canonical taskforce contract", name)
		}
	}
	for _, topic := range []string{"dual-lane-adjudication", "three-review"} {
		var out, stderr bytes.Buffer
		if code := Main(t.TempDir(), []string{"skills", topic}, &out, &stderr); code != 0 {
			t.Fatalf("legacy topic %s: %d %s", topic, code, stderr.String())
		}
		if !strings.Contains(out.String(), "spawn template=dual-lens") || !strings.Contains(out.String(), manifests.DualLensInstructions()) {
			t.Fatalf("legacy topic %s did not expose the canonical launcher and chair", topic)
		}
	}
}
