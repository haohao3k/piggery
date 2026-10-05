package cli

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

//go:embed skills/piggery-dual-lane-adjudication/SKILL.md
var dualLaneAdjudicationSkillMD string

// dualLaneAdjudicationSkill renders a small native entry point. Workflow instructions come from the
// installed CLI at invocation time, not an independently maintained copy in every harness.
func dualLaneAdjudicationSkill(self, name string) string {
	s := strings.Replace(dualLaneAdjudicationSkillMD, "name: piggery-dual-lane-adjudication", "name: "+name, 1)
	if self != "" {
		s = strings.ReplaceAll(s, "`piggery skills dual-lane-adjudication`", "`"+local.ShellQuote(self)+" skills dual-lane-adjudication`")
		s = strings.ReplaceAll(s, "`piggery skills`", "`"+local.ShellQuote(self)+" skills`")
	}
	return s
}

func (e *env) skills(args []string) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(e.stdout, skillsMD)
		return err
	}
	if len(args) != 1 || (args[0] != "dual-lane-adjudication" && args[0] != "three-review") {
		return fmt.Errorf("%w: skills accepts only the optional topic dual-lane-adjudication", errUsage)
	}
	_, err := fmt.Fprintf(e.stdout, "# Piggery dual-lane adjudication — build %s\n\n"+
		"This command prints the Lead playbook; it launches no workers. Use the task context or user scope.\n"+
		"Tool placeholders {tool:X} mean piggery_X in Pi, or mcp__piggery__X in Codex/Claude.\n"+
		"Bind each launch reply to the exact participant id and message number from the current assignment; never use a hardcoded recipient.\n\n%s",
		Version, manifests.DualLaneAdjudicationInstructions())
	return err
}

//go:embed skills/legacy-three-review.md
var legacyThreeReviewSkillMD string

func legacyThreeReviewSkill(self string) string {
	s := strings.Replace(legacyThreeReviewSkillMD, "name: piggery-three-review", "name: three-review", 1)
	s = strings.ReplaceAll(s, "`piggery skills three-review`", "`"+local.ShellQuote(self)+" skills three-review`")
	s = strings.ReplaceAll(s, "`piggery skills`", "`"+local.ShellQuote(self)+" skills`")
	return s + "\n\n## Invocation arguments\n\n$ARGUMENTS\n"
}
