package resources

import (
	"testing"

	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewAgentDeployment(t *testing.T) {
	replicas := int32(2)
	jenkins := &v1alpha2.Jenkins{
		ObjectMeta: metav1.ObjectMeta{Name: "jenkins", Namespace: "default"},
		Spec: v1alpha2.JenkinsSpec{
			Service: v1alpha2.Service{Port: 8080},
			Master: v1alpha2.JenkinsMaster{
				Containers: []v1alpha2.Container{{Name: JenkinsMasterContainerName}},
			},
		},
	}

	dep, err := NewAgentDeployment(jenkins, "cluster.local", AgentDeploymentParams{
		DeploymentName: "jenkins-build-agent",
		AgentName:      "build-agent",
		Namespace:      "default",
		Image:          "jenkins/inbound-agent:latest",
		Secret:         "sec",
		Replicas:       &replicas,
		DeploymentLabels: map[string]string{
			"jenkins.io/agent-name": "build-agent",
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "jenkins-build-agent", dep.Name)
	assert.Equal(t, "default", dep.Namespace)
	assert.Equal(t, int32(2), *dep.Spec.Replicas)
	assert.Equal(t, "build-agent", dep.Labels["jenkins.io/agent-name"])
	assert.Equal(t, "jenkins", dep.OwnerReferences[0].Name)

	c := dep.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "jnlp", c.Name)
	assert.Equal(t, "jenkins/inbound-agent:latest", c.Image)
	assertAgentEnv(t, c.Env, "JENKINS_AGENT_NAME", "build-agent")
	assertAgentEnv(t, c.Env, "JENKINS_SECRET", "sec")
	assertAgentEnv(t, c.Env, "JENKINS_WEB_SOCKET", "true")
}

func assertAgentEnv(t *testing.T, envs []corev1.EnvVar, name, value string) {
	t.Helper()
	for _, e := range envs {
		if e.Name == name {
			assert.Equal(t, value, e.Value)
			return
		}
	}
	t.Fatalf("env %q not found", name)
}
