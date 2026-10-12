package manifests

// AgentInstructions is the common guidance added to every role card, including solo sessions.
func AgentInstructions() string {
	b, _ := builtin.ReadFile("prompts/agent-ownership.md")
	return string(b)
}
