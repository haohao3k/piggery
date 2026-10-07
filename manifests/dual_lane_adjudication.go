package manifests

// DualLensInstructions shares the taskforce chair playbook with native skill entries.
func DualLensInstructions() string {
	b, _ := builtin.ReadFile("prompts/dual-lens/lead.md")
	return string(b)
}

// DualLaneAdjudicationInstructions is the legacy API name for the same playbook.
func DualLaneAdjudicationInstructions() string { return DualLensInstructions() }
