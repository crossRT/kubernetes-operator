package agents

import (
	"fmt"

	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"

	validationutil "k8s.io/apimachinery/pkg/util/validation"
)

// Validate verifies the spec.agents configuration and returns human-readable messages.
func (a *agents) Validate(jenkins *v1alpha2.Jenkins) []string {
	var messages []string
	seen := map[string]bool{}

	for _, agent := range jenkins.Spec.Agents {
		if agent.Name == "" {
			messages = append(messages, "spec.agents: agent name cannot be empty")
			continue
		}
		for _, e := range validationutil.IsDNS1123Label(agent.Name) {
			messages = append(messages, fmt.Sprintf("spec.agents: agent name '%s' is not a valid DNS label: %s", agent.Name, e))
		}
		if agent.Name == ReservedSeedJobAgentName {
			messages = append(messages, fmt.Sprintf("spec.agents: agent name '%s' is reserved", agent.Name))
		}
		if seen[agent.Name] {
			messages = append(messages, fmt.Sprintf("spec.agents: duplicate agent name '%s'", agent.Name))
		}
		seen[agent.Name] = true
		if agent.NumExecutors < 0 {
			messages = append(messages, fmt.Sprintf("spec.agents: agent '%s' numExecutors must be >= 0", agent.Name))
		}
	}
	return messages
}
