package agents

import (
	"testing"

	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	a := &agents{}
	jenkins := &v1alpha2.Jenkins{
		Spec: v1alpha2.JenkinsSpec{
			Agents: []v1alpha2.Agent{
				{Name: "good"},
				{Name: "good"},           // duplicate
				{Name: ""},               // empty
				{Name: "seed-job-agent"}, // reserved
				{Name: "Bad_Name"},       // invalid DNS label
			},
		},
	}

	msgs := a.Validate(jenkins)

	joined := ""
	for _, m := range msgs {
		joined += m + "\n"
	}
	assert.Contains(t, joined, "duplicate")
	assert.Contains(t, joined, "empty")
	assert.Contains(t, joined, "reserved")
	assert.Contains(t, joined, "not a valid DNS label")
}

func TestValidateNoAgentsReturnsEmpty(t *testing.T) {
	a := &agents{}
	jenkins := &v1alpha2.Jenkins{}
	assert.Empty(t, a.Validate(jenkins))
}
