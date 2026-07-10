package v1alpha2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestJenkinsSpecAgentsDeepCopy(t *testing.T) {
	replicas := int32(3)
	original := &Jenkins{
		Spec: JenkinsSpec{
			Agents: []Agent{
				{
					Name:         "build-agent",
					Image:        "jenkins/inbound-agent:latest",
					Replicas:     &replicas,
					NumExecutors: 4,
					Label:        "linux",
					NodeSelector: map[string]string{"arch": "amd64"},
					Tolerations:  []corev1.Toleration{{Key: "dedicated"}},
				},
			},
		},
	}

	copied := original.DeepCopy()

	assert.Equal(t, original.Spec.Agents, copied.Spec.Agents)
	*copied.Spec.Agents[0].Replicas = 7
	assert.Equal(t, int32(3), *original.Spec.Agents[0].Replicas)
	copied.Spec.Agents[0].NodeSelector["arch"] = "arm64"
	assert.Equal(t, "amd64", original.Spec.Agents[0].NodeSelector["arch"])
}
