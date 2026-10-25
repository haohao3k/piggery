package manifests

// DualLensInstructions shares the taskforce chair playbook with native skill entries.
func DualLensInstructions() string {
	b, _ := builtin.ReadFile("prompts/dual-lens/lead.md")
	return string(b)
}

// DualLaneAdjudicationInstructions is the legacy API name for the same playbook.
func DualLaneAdjudicationInstructions() string { return DualLensInstructions() }

// DualLensRoute is this fork's default routing, below every explicit config.yaml roles field.
// Keeping it outside manifests lets upstream's role migration leave built-ins unchanged.
func DualLensRoute(template, role string) (harness, model, thinking string) {
	switch template {
	case "dual-lens", "dual-lane-adjudication", "triple-review":
		switch role {
		case "lens-a":
			return "codex", "gpt-6-astra", "high"
		case "lens-b":
			return "claude", "claude-opus-5-5", "high"
		}
	}
	return "", "", ""
}
