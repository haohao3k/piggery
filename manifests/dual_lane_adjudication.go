package manifests

// DualLaneAdjudicationInstructions shares the Lead playbook with native skill entries.
func DualLaneAdjudicationInstructions() string {
	b, _ := builtin.ReadFile("prompts/dual-lane-adjudication/lead.md")
	return string(b)
}
