package resources

import (
	"fmt"

	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	agentHomeVolumeName      = "home"
	agentHomeVolumePath      = "/home/jenkins/agent"
	agentWorkspaceVolumeName = "workspace"
	agentWorkspaceVolumePath = "/home/jenkins/workspace"
)

// AgentDeploymentParams holds the values needed to build an inbound (JNLP) agent Deployment.
type AgentDeploymentParams struct {
	DeploymentName   string
	AgentName        string
	Namespace        string
	Image            string
	Secret           string
	Replicas         *int32
	Resources        corev1.ResourceRequirements
	NodeSelector     map[string]string
	Tolerations      []corev1.Toleration
	ImagePullSecrets []corev1.LocalObjectReference
	HostAliases      []corev1.HostAlias
	DeploymentLabels map[string]string
}

// NewAgentDeployment builds a Kubernetes Deployment for an inbound Jenkins agent that
// connects back to the Jenkins master. Shared by the seed-job agent and operator-managed
// persistent agents.
func NewAgentDeployment(jenkins *v1alpha2.Jenkins, kubernetesClusterDomain string, p AgentDeploymentParams) (*appsv1.Deployment, error) {
	jenkinsHTTPServiceFQDN, err := GetJenkinsHTTPServiceFQDN(jenkins, kubernetesClusterDomain)
	if err != nil {
		return nil, err
	}

	suffix := ""
	if prefix, ok := GetJenkinsOpts(*jenkins)["prefix"]; ok {
		suffix = prefix
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.DeploymentName,
			Namespace: p.Namespace,
			Labels:    p.DeploymentLabels,
			OwnerReferences: []metav1.OwnerReference{
				{
					BlockOwnerDeletion: &[]bool{true}[0],
					Controller:         &[]bool{true}[0],
					Kind:               jenkins.Kind,
					Name:               jenkins.Name,
					APIVersion:         jenkins.APIVersion,
					UID:                jenkins.UID,
				},
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: p.Replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector:     p.NodeSelector,
					Tolerations:      p.Tolerations,
					ImagePullSecrets: p.ImagePullSecrets,
					HostAliases:      p.HostAliases,
					Containers: []corev1.Container{
						{
							Name:      "jnlp",
							Image:     p.Image,
							Resources: p.Resources,
							Env: []corev1.EnvVar{
								{Name: "JENKINS_WEB_SOCKET", Value: "true"},
								{Name: "JENKINS_SECRET", Value: p.Secret},
								{Name: "JENKINS_AGENT_NAME", Value: p.AgentName},
								{
									Name: "JENKINS_URL",
									Value: fmt.Sprintf("http://%s:%d%s",
										jenkinsHTTPServiceFQDN,
										jenkins.Spec.Service.Port,
										suffix),
								},
								{Name: "JENKINS_AGENT_WORKDIR", Value: agentHomeVolumePath},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: agentHomeVolumeName, MountPath: agentHomeVolumePath},
								{Name: agentWorkspaceVolumeName, MountPath: agentWorkspaceVolumePath},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: agentHomeVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: agentWorkspaceVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": fmt.Sprintf("%s-selector", p.AgentName),
					},
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": fmt.Sprintf("%s-selector", p.AgentName),
				},
			},
		},
	}, nil
}
