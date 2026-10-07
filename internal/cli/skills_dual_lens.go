package cli

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

//go:embed skills/piggery-dual-lens/SKILL.md
var dualLensSkillMD string

// dualLensSkill renders a small native entry point. Workflow instructions come from the
// installed CLI at invocation time, not an independently maintained copy in every harness.
func dualLensSkill(self, name string) string {
	s := strings.Replace(dualLensSkillMD, "name: piggery-dual-lens", "name: "+name, 1)
	if self != "" {
		s = strings.ReplaceAll(s, "`piggery skills dual-lens`", "`"+local.ShellQuote(self)+" skills dual-lens`")
		s = strings.ReplaceAll(s, "`piggery skills`", "`"+local.ShellQuote(self)+" skills`")
	}
	return s
}

func (e *env) skills(args []string) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(e.stdout, skillsMD)
		return err
	}
	if len(args) != 1 || (args[0] != "dual-lens" && args[0] != "dual-lane-adjudication" && args[0] != "three-review") {
		return fmt.Errorf("%w: skills accepts only the optional topic dual-lens", errUsage)
	}
	_, err := fmt.Fprintf(e.stdout, "# Piggery dual-lens — build %s\n\n"+
		"This command only prints instructions; it launches no workers.\n\n"+
		"## Caller workflow\n\n"+
		"Use the current hard question or supplied scope. A prepare-only request ends at the brief.\n"+
		"When authorized, a solo or gate calls spawn template=dual-lens with the constraints, evidence and input revision.\n"+
		"Stay in your current session/team; never found, leave or replace a team to call a taskforce.\n"+
		"A nongate uses its permitted route to the gate. Missing taskforce tools are a blocker.\n"+
		"Wait for the chair's answer by mail; close only that taskforce when finished. Review grants no edit or release authority.\n\n"+
		"## Chair role reference\n\n"+
		"Tool placeholders {tool:X} mean piggery_X in Pi, or mcp__piggery__X in Codex/Claude.\n"+
		"Bind each launch reply to the exact participant id and message number from the current assignment; never use a hardcoded recipient.\n\n%s",
		Version, manifests.DualLensInstructions())
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

//go:embed skills/legacy-dual-lane-adjudication.md
var legacyDualLaneSkillMD string

func legacyDualLaneClaudeSkill(self string) string {
	s := strings.Replace(legacyDualLaneSkillMD, "name: piggery-dual-lane-adjudication", "name: dual-lane-adjudication", 1)
	s = strings.ReplaceAll(s, "`piggery skills dual-lane-adjudication`", "`"+local.ShellQuote(self)+" skills dual-lane-adjudication`")
	s = strings.ReplaceAll(s, "`piggery skills`", "`"+local.ShellQuote(self)+" skills`")
	return s + "\n\n## Invocation arguments\n\n$ARGUMENTS\n"
}
