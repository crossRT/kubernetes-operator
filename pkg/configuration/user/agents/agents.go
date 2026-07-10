package agents

import (
	"context"
	"fmt"

	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"
	jenkinsclient "github.com/jenkinsci/kubernetes-operator/pkg/client"
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration"
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration/base/resources"
	"github.com/jenkinsci/kubernetes-operator/pkg/log"

	"github.com/go-logr/logr"
	stackerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// defaultAgentImage is the default inbound agent image.
	defaultAgentImage = "jenkins/inbound-agent:3248.v65ecb_254c298-6"

	// ManagedByLabel marks Deployments the operator creates for agents.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// ManagedByValue is the value of ManagedByLabel.
	ManagedByValue = "jenkins-operator"
	// AgentCRLabel identifies the owning Jenkins CR by name.
	AgentCRLabel = "jenkins.io/agent-cr"
	// AgentNameLabel identifies the agent by name.
	AgentNameLabel = "jenkins.io/agent-name"

	// ReservedSeedJobAgentName is the seed-job agent name that user agents must not reuse.
	ReservedSeedJobAgentName = "seed-job-agent"

	agentRemoteFS    = "/home/jenkins"
	noNodeFoundError = "No node found"
)

// Agents reconciles operator-managed persistent agents.
type Agents interface {
	EnsureAgents(jenkins *v1alpha2.Jenkins) (done bool, err error)
	Validate(jenkins *v1alpha2.Jenkins) []string
}

type agents struct {
	configuration.Configuration
	jenkinsClient jenkinsclient.Jenkins
	logger        logr.Logger
}

// New creates an Agents object.
func New(jenkinsClient jenkinsclient.Jenkins, config configuration.Configuration) Agents {
	return &agents{
		Configuration: config,
		jenkinsClient: jenkinsClient,
		logger:        log.Log.WithValues("cr", config.Jenkins.Name),
	}
}

// AgentDeploymentName returns the Deployment name for an agent: <jenkinsName>-<agentName>.
func AgentDeploymentName(jenkinsName, agentName string) string {
	return fmt.Sprintf("%s-%s", jenkinsName, agentName)
}

// EnsureAgents reconciles every agent in spec.agents and prunes removed ones.
func (a *agents) EnsureAgents(jenkins *v1alpha2.Jenkins) (bool, error) {
	for i := range jenkins.Spec.Agents {
		if err := a.ensureAgent(jenkins, &jenkins.Spec.Agents[i]); err != nil {
			return false, err
		}
	}
	if err := a.pruneRemovedAgents(jenkins); err != nil {
		return false, err
	}
	return true, nil
}

func (a *agents) ensureAgent(jenkins *v1alpha2.Jenkins, agent *v1alpha2.Agent) error {
	name := agent.Name

	numExecutors := 1
	if agent.NumExecutors > 0 {
		numExecutors = agent.NumExecutors
	}
	label := agent.Label
	if label == "" {
		label = name
	}
	image := agent.Image
	if image == "" {
		image = defaultAgentImage
	}
	replicas := agent.Replicas
	if replicas == nil {
		one := int32(1)
		replicas = &one
	}

	_, err := a.jenkinsClient.GetNode(context.TODO(), name)
	if err != nil && err.Error() == noNodeFoundError {
		if _, err := a.jenkinsClient.CreateNode(context.TODO(), name, numExecutors, "operator-managed agent", agentRemoteFS, label); err != nil {
			return stackerr.WithStack(err)
		}
	} else if err != nil {
		return stackerr.WithStack(err)
	}

	secret, err := a.jenkinsClient.GetNodeSecret(name)
	if err != nil {
		return err
	}

	deployment, err := resources.NewAgentDeployment(jenkins, a.KubernetesClusterDomain, resources.AgentDeploymentParams{
		DeploymentName:   AgentDeploymentName(jenkins.Name, name),
		AgentName:        name,
		Namespace:        jenkins.Namespace,
		Image:            image,
		Secret:           secret,
		Replicas:         replicas,
		Resources:        agent.Resources,
		NodeSelector:     agent.NodeSelector,
		Tolerations:      agent.Tolerations,
		ImagePullSecrets: jenkins.Spec.Master.ImagePullSecrets,
		HostAliases:      jenkins.Spec.Master.HostAliases,
		DeploymentLabels: map[string]string{
			ManagedByLabel: ManagedByValue,
			AgentCRLabel:   jenkins.Name,
			AgentNameLabel: name,
		},
	})
	if err != nil {
		return err
	}

	err = a.Client.Create(context.TODO(), deployment)
	if apierrors.IsAlreadyExists(err) {
		if err := a.Client.Update(context.TODO(), deployment); err != nil {
			return stackerr.WithStack(err)
		}
	} else if err != nil {
		return stackerr.WithStack(err)
	}
	return nil
}

func (a *agents) pruneRemovedAgents(jenkins *v1alpha2.Jenkins) error {
	desired := map[string]bool{}
	for _, agent := range jenkins.Spec.Agents {
		desired[agent.Name] = true
	}

	var deployments appsv1.DeploymentList
	err := a.Client.List(context.TODO(), &deployments,
		client.InNamespace(jenkins.Namespace),
		client.MatchingLabels{
			ManagedByLabel: ManagedByValue,
			AgentCRLabel:   jenkins.Name,
		},
	)
	if err != nil {
		return stackerr.WithStack(err)
	}

	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		agentName := deployment.Labels[AgentNameLabel]
		if desired[agentName] {
			continue
		}
		if err := a.Client.Delete(context.TODO(), deployment); err != nil && !apierrors.IsNotFound(err) {
			return stackerr.WithStack(err)
		}
		if _, err := a.jenkinsClient.DeleteNode(context.TODO(), agentName); err != nil && err.Error() != noNodeFoundError {
			return stackerr.WithStack(err)
		}
	}
	return nil
}
