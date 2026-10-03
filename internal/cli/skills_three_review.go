package cli

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/sting8k/piggery/internal/driver/local"
	"github.com/sting8k/piggery/manifests"
)

//go:embed skills/piggery-three-review/SKILL.md
var threeReviewSkillMD string

// threeReviewSkill renders a small native entry point. Workflow instructions come from the
// installed CLI at invocation time, not an independently maintained copy in every harness.
func threeReviewSkill(self, name string) string {
	s := strings.Replace(threeReviewSkillMD, "name: piggery-three-review", "name: "+name, 1)
	if self != "" {
		s = strings.ReplaceAll(s, "`piggery skills three-review`", "`"+local.ShellQuote(self)+" skills three-review`")
		s = strings.ReplaceAll(s, "`piggery skills`", "`"+local.ShellQuote(self)+" skills`")
	}
	return s
}

func (e *env) skills(args []string) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(e.stdout, skillsMD)
		return err
	}
	if len(args) != 1 || args[0] != "three-review" {
		return fmt.Errorf("%w: skills accepts only the optional topic three-review", errUsage)
	}
	_, err := fmt.Fprintf(e.stdout, "# Piggery three-arm review — build %s\n\n"+
		"This is a printed playbook, not a launched review. Use the user's scope text.\n"+
		"Tool placeholders {tool:X} mean piggery_X in Pi, or mcp__piggery__X in Codex/Claude.\n\n%s",
		Version, manifests.TripleReviewInstructions())
	return err
}
