package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sting8k/piggery/manifests"
	"gopkg.in/yaml.v3"
)

func TestThreeReviewSkillPrintsEmbeddedCoordinatorWithoutDaemon(t *testing.T) {
	// Printing a workflow must work before setup, even in a participant shell. It must not
	// create a home, connect to a daemon, or turn a skill lookup into a review run.
	t.Setenv("PIGGERY_ID", "unused-participant")
	t.Setenv("PIGGERY_TOKEN", "unused-token")
	home := filepath.Join(t.TempDir(), "not-created")
	var out, stderr bytes.Buffer
	if code := Main(home, []string{"skills", "three-review"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("printing a skill touched the Piggery home: %v", err)
	}
	manifest, err := manifests.Builtin("triple-review")
	if err != nil {
		t.Fatal(err)
	}
	var team struct {
		Roles map[string]struct{ Instructions string }
	}
	if err := yaml.Unmarshal([]byte(manifest), &team); err != nil {
		t.Fatal(err)
	}
	playbook := team.Roles["coordinator"].Instructions
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
		{"extra", []string{"skills", "three-review", "execute"}, 2},
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

func TestThreeReviewPlaybookBindsLaunchAndRoot(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Main(t.TempDir(), []string{"skills", "three-review"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	playbook := out.String()
	for _, want := range []string{
		"pwd -P",
		"git -C <candidate-root> rev-parse --show-toplevel",
		"session binding root",
		"not the candidate Git root",
		"participant id",
		"assignment's `#N`",
		"Never hardcode `coordinator`",
	} {
		if !strings.Contains(playbook, want) {
			t.Fatalf("playbook missing %q", want)
		}
	}
	if strings.Contains(playbook, "to: notify") || strings.Contains(playbook, "to=coordinator") {
		t.Fatal("playbook contains a fixed notify or coordinator recipient")
	}
	manifest, err := manifests.Builtin("triple-review")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(manifest, "to: notify") {
		t.Fatal("triple-review manifest routes agent mail to notify")
	}
	home := t.TempDir()
	if err := manifests.Unpack(home); err != nil {
		t.Fatal(err)
	}
	resolved, err := manifests.Resolve("triple-review", home)
	if err != nil {
		t.Fatal(err)
	}
	var team struct {
		Roles map[string]struct {
			Instructions string
		}
	}
	if err := yaml.Unmarshal([]byte(resolved), &team); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"coordinator", "semantic_a", "coverage"} {
		instructions := team.Roles[role].Instructions
		for _, want := range []string{"return_to", "piggery ps --json", "participant id", "unknown or gone", "need not equal"} {
			if !strings.Contains(instructions, want) {
				t.Fatalf("%s prompt missing %q", role, want)
			}
		}
	}
}
