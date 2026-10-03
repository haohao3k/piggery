package manifests

// TripleReviewInstructions returns the coordinator playbook embedded in this build. The CLI
// skill entry and the team role share these bytes, so preparation and review cannot drift.
func TripleReviewInstructions() string {
	b, _ := builtin.ReadFile("prompts/triple-review/coordinator.md")
	return string(b)
}
