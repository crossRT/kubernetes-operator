# Operator-Managed Persistent Agents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a declarative `spec.agents[]` field so the operator registers and runs persistent inbound (JNLP) Jenkins agents.

**Architecture:** Generalize the existing seed-job agent into a reusable Deployment builder in `pkg/configuration/base/resources`, then add a new `pkg/configuration/user/agents` package that registers each agent as a Jenkins node (imperative API) and reconciles its Deployment, pruning removed agents. Hook it into the user reconcile loop.

**Tech Stack:** Go 1.22, controller-runtime, controller-gen v0.14.0, gomock, controller-runtime fake client, testify.

## Global Constraints

- Go 1.22; `controller-gen` binary at `./bin/controller-gen` (v0.14.0).
- Regenerate deepcopy: `./bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."`
- Regenerate CRD base: `./bin/controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases`
- `deploy/crds/jenkins.io_jenkins_crd.yaml` and `chart/jenkins-operator/crds/jenkins-crd.yaml` are compact CRD copies; master-level properties are indented 18 spaces in **all three** CRD files, so schema subtrees splice between them without reindentation.
- Do **not** change the seed-job agent's behavior or its Deployment name (`<agentName>-<jenkinsName>`).
- New agents use Deployment name `<jenkinsName>-<agentName>`.
- Reserved agent name that user agents must not use: `seed-job-agent`.
- Commit after each task.

---

### Task 1: Add `Agent` type and `spec.agents` to the API

**Files:**
- Modify: `api/v1alpha2/jenkins_types.go` (add `Agent` struct; add `Agents` field to `JenkinsSpec`)
- Modify (generated): `api/v1alpha2/zz_generated.deepcopy.go`
- Test: `api/v1alpha2/agents_test.go`

**Interfaces:**
- Produces: `v1alpha2.Agent{ Name string; Image string; Replicas *int32; NumExecutors int; Label string; Resources corev1.ResourceRequirements; NodeSelector map[string]string; Tolerations []corev1.Toleration }` and `JenkinsSpec.Agents []Agent`.

- [ ] **Step 1: Write the failing test**

Create `api/v1alpha2/agents_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./api/v1alpha2/ -run TestJenkinsSpecAgentsDeepCopy`
Expected: FAIL — compile error `undefined: Agent`.

- [ ] **Step 3: Add the `Agent` type and `Agents` field**

In `api/v1alpha2/jenkins_types.go`, add the field to `JenkinsSpec` (place after the existing `Master JenkinsMaster` field; keep exact existing fields intact):

```go
	// Agents defines persistent inbound (JNLP) Jenkins agents managed by the operator.
	// Each agent is registered as a Jenkins node and run as a Kubernetes Deployment.
	// Node registration is stored in $JENKINS_HOME; use spec.master.persistentVolumeClaim
	// for a PVC-backed $JENKINS_HOME to persist agents across master restarts.
	// +optional
	Agents []Agent `json:"agents,omitempty"`
```

Add the new type near the other spec types (e.g. after the `JenkinsMaster` struct):

```go
// Agent defines a persistent inbound (JNLP) Jenkins agent managed by the operator.
type Agent struct {
	// Name is the unique agent name. Used as the Jenkins node name and the basis
	// for the agent Deployment name (<jenkinsName>-<name>). Must be a DNS label.
	Name string `json:"name"`

	// Image is the inbound agent image. Defaults to the operator's pinned
	// jenkins/inbound-agent image when empty.
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas is the number of agent pods. Defaults to 1.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=255
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// NumExecutors is the number of executors the Jenkins node exposes. Defaults to 1.
	// +optional
	NumExecutors int `json:"numExecutors,omitempty"`

	// Label is the Jenkins label(s) assigned to the node, space-separated.
	// Defaults to Name when empty.
	// +optional
	Label string `json:"label,omitempty"`

	// Resources are the compute resources for the agent container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector for the agent pod.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for the agent pod.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}
```

- [ ] **Step 4: Regenerate deepcopy**

Run: `./bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."`
Expected: no output, exit 0. `git diff api/v1alpha2/zz_generated.deepcopy.go` shows new `Agent` DeepCopy funcs and `Agents` handling in `JenkinsSpec.DeepCopyInto`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./api/v1alpha2/ -run TestJenkinsSpecAgentsDeepCopy`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/v1alpha2/jenkins_types.go api/v1alpha2/zz_generated.deepcopy.go api/v1alpha2/agents_test.go
git commit -m "feat: add spec.agents Agent type to Jenkins CR"
```

---

### Task 2: Shared agent Deployment builder + seed-job refactor

**Files:**
- Create: `pkg/configuration/base/resources/agent.go`
- Modify: `pkg/configuration/user/seedjobs/seedjobs.go` (replace `agentDeployment` body; remove the four now-unused volume constants)
- Test: `pkg/configuration/base/resources/agent_test.go`

**Interfaces:**
- Consumes: `resources.GetJenkinsHTTPServiceFQDN(*v1alpha2.Jenkins, string) (string, error)`, `resources.GetJenkinsOpts(v1alpha2.Jenkins) map[string]string`.
- Produces: `resources.AgentDeploymentParams` struct and `resources.NewAgentDeployment(jenkins *v1alpha2.Jenkins, kubernetesClusterDomain string, p AgentDeploymentParams) (*appsv1.Deployment, error)`.

- [ ] **Step 1: Write the failing test**

Create `pkg/configuration/base/resources/agent_test.go`:

```go
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
		Spec:       v1alpha2.JenkinsSpec{Service: v1alpha2.Service{Port: 8080}},
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/configuration/base/resources/ -run TestNewAgentDeployment`
Expected: FAIL — compile error `undefined: NewAgentDeployment`.

- [ ] **Step 3: Create the shared builder**

Create `pkg/configuration/base/resources/agent.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/configuration/base/resources/ -run TestNewAgentDeployment`
Expected: PASS.

- [ ] **Step 5: Refactor seed-job to use the shared builder**

In `pkg/configuration/user/seedjobs/seedjobs.go`, replace the entire `agentDeployment` function (currently ~lines 414-520) with:

```go
func agentDeployment(jenkins *v1alpha2.Jenkins, namespace string, agentName string, secret string, kubernetesDomainName string) (*appsv1.Deployment, error) {
	agentImage := jenkins.Spec.SeedJobAgentImage
	if jenkins.Spec.SeedJobAgentImage == "" {
		agentImage = defaultAgentImage
	}
	return resources.NewAgentDeployment(jenkins, kubernetesDomainName, resources.AgentDeploymentParams{
		DeploymentName:   agentDeploymentName(*jenkins, agentName),
		AgentName:        agentName,
		Namespace:        namespace,
		Image:            agentImage,
		Secret:           secret,
		NodeSelector:     jenkins.Spec.Master.NodeSelector,
		Tolerations:      jenkins.Spec.Master.Tolerations,
		ImagePullSecrets: jenkins.Spec.Master.ImagePullSecrets,
		HostAliases:      jenkins.Spec.Master.HostAliases,
	})
}
```

Then remove the now-unused constants from the `const (...)` block in `seedjobs.go` (the shared builder owns them now):

```go
	homeVolumeName = "home"
	homeVolumePath = "/home/jenkins/agent"

	workspaceVolumeName = "workspace"
	workspaceVolumePath = "/home/jenkins/workspace"
```

- [ ] **Step 6: Fix imports and build**

Run: `goimports -w pkg/configuration/user/seedjobs/seedjobs.go` (or remove any now-unused imports the compiler flags: `corev1`, `metav1` may still be used elsewhere in the file — only remove what the build reports).
Run: `go build ./...`
Expected: exit 0. If the build reports an unused import in `seedjobs.go`, remove exactly that import and rebuild.

- [ ] **Step 7: Run seed-job tests to confirm unchanged behavior**

Run: `go test ./pkg/configuration/user/seedjobs/... ./pkg/configuration/base/resources/...`
Expected: PASS (existing seed-job tests still green — the Deployment output is unchanged).

- [ ] **Step 8: Commit**

```bash
git add pkg/configuration/base/resources/agent.go pkg/configuration/base/resources/agent_test.go pkg/configuration/user/seedjobs/seedjobs.go
git commit -m "refactor: extract shared inbound agent Deployment builder"
```

---

### Task 3: `agents` package — EnsureAgents (register, deploy, prune)

**Files:**
- Create: `pkg/configuration/user/agents/agents.go`
- Create: `pkg/configuration/user/agents/doc.go`
- Test: `pkg/configuration/user/agents/agents_test.go`

**Interfaces:**
- Consumes: `resources.NewAgentDeployment(...)`, `jenkinsclient.Jenkins` (`GetNode`, `CreateNode`, `GetNodeSecret`, `DeleteNode`), `configuration.Configuration` (`Client`, `KubernetesClusterDomain`, `Jenkins`).
- Produces: `agents.New(jenkinsClient jenkinsclient.Jenkins, config configuration.Configuration) Agents`; `Agents` interface with `EnsureAgents(*v1alpha2.Jenkins) (bool, error)` (Task 4 extends it with `Validate`); `agents.AgentDeploymentName(jenkinsName, agentName string) string`; exported constants `ManagedByLabel`, `ManagedByValue`, `AgentCRLabel`, `AgentNameLabel`, `ReservedSeedJobAgentName`.

- [ ] **Step 1: Write the failing test**

Create `pkg/configuration/user/agents/agents_test.go`:

```go
package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"
	jenkinsclient "github.com/jenkinsci/kubernetes-operator/pkg/client"
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureAgents(t *testing.T) {
	err := v1alpha2.SchemeBuilder.AddToScheme(scheme.Scheme)
	assert.NoError(t, err)

	t.Run("creates node and deployment for a new agent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ctx := context.TODO()

		jenkins := &v1alpha2.Jenkins{
			ObjectMeta: metav1.ObjectMeta{Name: "jenkins", Namespace: "default"},
			Spec: v1alpha2.JenkinsSpec{
				Service: v1alpha2.Service{Port: 8080},
				Agents:  []v1alpha2.Agent{{Name: "build-agent", NumExecutors: 2}},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

		jc := jenkinsclient.NewMockJenkins(ctrl)
		jc.EXPECT().GetNode(ctx, "build-agent").Return(nil, errors.New("No node found"))
		jc.EXPECT().CreateNode(ctx, "build-agent", 2, "operator-managed agent", "/home/jenkins", "build-agent").Return(nil, nil)
		jc.EXPECT().GetNodeSecret("build-agent").Return("secret", nil)

		client := New(jc, configuration.Configuration{Client: fakeClient, Jenkins: jenkins})
		done, err := client.EnsureAgents(jenkins)
		assert.NoError(t, err)
		assert.True(t, done)

		var dep appsv1.Deployment
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "jenkins-build-agent"}, &dep)
		assert.NoError(t, err)
		assert.Equal(t, ManagedByValue, dep.Labels[ManagedByLabel])
		assert.Equal(t, "build-agent", dep.Labels[AgentNameLabel])
		assert.Equal(t, int32(1), *dep.Spec.Replicas)
	})

	t.Run("prunes deployment and node for a removed agent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ctx := context.TODO()

		jenkins := &v1alpha2.Jenkins{
			ObjectMeta: metav1.ObjectMeta{Name: "jenkins", Namespace: "default"},
			Spec:       v1alpha2.JenkinsSpec{Agents: []v1alpha2.Agent{}},
		}
		stale := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jenkins-old-agent",
				Namespace: "default",
				Labels: map[string]string{
					ManagedByLabel: ManagedByValue,
					AgentCRLabel:   "jenkins",
					AgentNameLabel: "old-agent",
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(stale).Build()

		jc := jenkinsclient.NewMockJenkins(ctrl)
		jc.EXPECT().DeleteNode(ctx, "old-agent").Return(true, nil)

		client := New(jc, configuration.Configuration{Client: fakeClient, Jenkins: jenkins})
		done, err := client.EnsureAgents(jenkins)
		assert.NoError(t, err)
		assert.True(t, done)

		var dep appsv1.Deployment
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "jenkins-old-agent"}, &dep)
		assert.True(t, apierrors.IsNotFound(err))
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/configuration/user/agents/ -run TestEnsureAgents`
Expected: FAIL — package/`New` undefined.

- [ ] **Step 3: Create the package doc**

Create `pkg/configuration/user/agents/doc.go`:

```go
// Package agents reconciles persistent inbound (JNLP) Jenkins agents declared in spec.agents.
package agents
```

- [ ] **Step 4: Implement `agents.go`**

Create `pkg/configuration/user/agents/agents.go`:

```go
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
// Task 4 extends this interface with Validate.
type Agents interface {
	EnsureAgents(jenkins *v1alpha2.Jenkins) (done bool, err error)
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/configuration/user/agents/ -run TestEnsureAgents`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/configuration/user/agents/
git commit -m "feat: reconcile persistent agents (register node, deploy, prune)"
```

---

### Task 4: Agent validation

**Files:**
- Create: `pkg/configuration/user/agents/validate.go`
- Modify: `pkg/configuration/user/validate.go` (accumulate agent messages)
- Test: `pkg/configuration/user/agents/validate_test.go`

**Interfaces:**
- Consumes: `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Label(string) []string`.
- Produces: `(a *agents) Validate(jenkins *v1alpha2.Jenkins) []string`, added to the `Agents` interface in `agents.go`.

- [ ] **Step 1: Write the failing test**

Create `pkg/configuration/user/agents/validate_test.go`:

```go
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
				{Name: "good"},              // duplicate
				{Name: ""},                  // empty
				{Name: "seed-job-agent"},    // reserved
				{Name: "Bad_Name"},          // invalid DNS label
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/configuration/user/agents/ -run TestValidate`
Expected: FAIL — `a.Validate` undefined.

- [ ] **Step 3: Add `Validate` to the `Agents` interface**

In `pkg/configuration/user/agents/agents.go`, add `Validate` to the interface so callers holding the `Agents` type can invoke it:

```go
// Agents reconciles operator-managed persistent agents.
type Agents interface {
	EnsureAgents(jenkins *v1alpha2.Jenkins) (done bool, err error)
	Validate(jenkins *v1alpha2.Jenkins) []string
}
```

- [ ] **Step 4: Implement validation**

Create `pkg/configuration/user/agents/validate.go`:

```go
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/configuration/user/agents/ -run TestValidate`
Expected: PASS.

- [ ] **Step 6: Wire agent validation into the user Validate**

Replace the body of `pkg/configuration/user/validate.go` with (accumulating all messages instead of returning early):

```go
package user

import (
	"github.com/jenkinsci/kubernetes-operator/api/v1alpha2"
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration/backuprestore"
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration/user/agents"
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration/user/seedjobs"
)

// Validate validates Jenkins CR Spec section
func (r *reconcileUserConfiguration) Validate(jenkins *v1alpha2.Jenkins) ([]string, error) {
	var messages []string

	backupAndRestore := backuprestore.New(r.Configuration, r.logger)
	messages = append(messages, backupAndRestore.Validate()...)

	seedJobs := seedjobs.New(r.jenkinsClient, r.Configuration)
	seedJobMessages, err := seedJobs.ValidateSeedJobs(*jenkins)
	if err != nil {
		return nil, err
	}
	messages = append(messages, seedJobMessages...)

	agentsClient := agents.New(r.jenkinsClient, r.Configuration)
	messages = append(messages, agentsClient.Validate(jenkins)...)

	return messages, nil
}
```

- [ ] **Step 7: Build and run package tests**

Run: `go build ./... && go test ./pkg/configuration/user/...`
Expected: exit 0, PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/configuration/user/agents/validate.go pkg/configuration/user/agents/validate_test.go pkg/configuration/user/agents/agents.go pkg/configuration/user/validate.go
git commit -m "feat: validate spec.agents configuration"
```

---

### Task 5: Hook EnsureAgents into the reconcile loop

**Files:**
- Modify: `pkg/configuration/user/reconcile.go`

**Interfaces:**
- Consumes: `agents.New(...).EnsureAgents(...)`.

- [ ] **Step 1: Add the reconcile step**

In `pkg/configuration/user/reconcile.go`, add the `agents` import to the import block:

```go
	"github.com/jenkinsci/kubernetes-operator/pkg/configuration/user/agents"
```

In `ReconcileOthers`, add the agents step after the seed-jobs step and before the final `return` (the seed-jobs block currently ends around line 77):

```go
	result, err = r.ensureAgents()
	if err != nil {
		return reconcile.Result{}, err
	}
	if result.Requeue {
		return result, nil
	}
```

Note: change the existing `result, err := r.ensureSeedJobs()` to reuse `result` — the seed-jobs call already declares `result` and `err`, so the agents call uses `=` (not `:=`) as shown above.

Add the helper method near `ensureSeedJobs`:

```go
func (r *reconcileUserConfiguration) ensureAgents() (reconcile.Result, error) {
	agentsClient := agents.New(r.jenkinsClient, r.Configuration)
	done, err := agentsClient.EnsureAgents(r.Configuration.Jenkins)
	if err != nil {
		return reconcile.Result{}, err
	}
	if !done {
		return reconcile.Result{Requeue: true}, nil
	}
	return reconcile.Result{}, nil
}
```

- [ ] **Step 2: Build and run the user config tests**

Run: `go build ./... && go vet ./pkg/configuration/user/... && go test ./pkg/configuration/user/...`
Expected: exit 0, PASS.

- [ ] **Step 3: Commit**

```bash
git add pkg/configuration/user/reconcile.go
git commit -m "feat: reconcile spec.agents in user configuration loop"
```

---

### Task 6: Regenerate CRDs, sync compact copies, docs

**Files:**
- Modify (generated): `config/crd/bases/jenkins.io_jenkins.yaml`
- Modify: `deploy/crds/jenkins.io_jenkins_crd.yaml`
- Modify: `chart/jenkins-operator/crds/jenkins-crd.yaml`
- Create: `docs/getting-started/latest/managing-agents.md` (short usage doc)

- [ ] **Step 1: Regenerate the CRD base**

Run: `./bin/controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases`
Expected: exit 0. Confirm the field is present:
Run: `grep -n "^                  agents:" config/crd/bases/jenkins.io_jenkins.yaml`
Expected: one match (agents sits first alphabetically among master properties).

- [ ] **Step 2: Extract the generated `agents` schema block**

The block spans from the `agents:` line to just before the next master-level property (both at 18-space indent).

Run:
```bash
awk '
  /^                  agents:/ {capture=1}
  capture && /^                  [a-zA-Z]/ && !/^                  agents:/ {capture=0}
  capture {print}
' config/crd/bases/jenkins.io_jenkins.yaml > /tmp/agents_block.yaml
head -3 /tmp/agents_block.yaml
wc -l /tmp/agents_block.yaml
```
Expected: first line is `                  agents:`, non-zero line count.

- [ ] **Step 3: Splice the block into both compact CRDs**

`agents` is alphabetically first, so insert it immediately before the first master-level `annotations:` property (18-space indent). Run:
```bash
for f in deploy/crds/jenkins.io_jenkins_crd.yaml chart/jenkins-operator/crds/jenkins-crd.yaml; do
  awk '
    /^                  annotations:/ && !inserted {
      while ((getline line < "/tmp/agents_block.yaml") > 0) print line
      close("/tmp/agents_block.yaml")
      inserted=1
    }
    { print }
  ' "$f" > "$f.tmp" && mv "$f.tmp" "$f"
done
grep -c "^                  agents:" deploy/crds/jenkins.io_jenkins_crd.yaml chart/jenkins-operator/crds/jenkins-crd.yaml
```
Expected: `1` for each file.

- [ ] **Step 4: Validate all three CRDs parse and expose the field**

Run:
```bash
cat > /tmp/yamlcheck.go <<'EOF'
package main
import ("fmt";"os";"sigs.k8s.io/yaml")
func main(){
 for _,f:=range os.Args[1:]{
  b,_:=os.ReadFile(f)
  var m map[string]interface{}
  if err:=yaml.Unmarshal(b,&m);err!=nil{fmt.Println("FAIL",f,err);os.Exit(1)}
  fmt.Println("OK",f)
 }
}
EOF
go run /tmp/yamlcheck.go config/crd/bases/jenkins.io_jenkins.yaml deploy/crds/jenkins.io_jenkins_crd.yaml chart/jenkins-operator/crds/jenkins-crd.yaml
```
Expected: three `OK` lines.

- [ ] **Step 5: Write the usage doc**

Create `docs/getting-started/latest/managing-agents.md`:

```markdown
# Managing Persistent Agents

The operator can run persistent inbound (JNLP) agents declared in `spec.agents`.
Each entry is registered as a Jenkins node and run as a Kubernetes Deployment
named `<jenkinsName>-<agentName>`.

```yaml
apiVersion: jenkins.io/v1alpha2
kind: Jenkins
metadata:
  name: example
spec:
  agents:
    - name: build-agent
      image: jenkins/inbound-agent:latest
      replicas: 2
      numExecutors: 4
      label: "linux docker"
      resources:
        requests: { cpu: "1", memory: 1Gi }
        limits: { cpu: "2", memory: 2Gi }
```

Target the agent from a pipeline with its label, e.g. `agent { label 'linux' }`
(defaults to the agent name when `label` is omitted).

Removing an entry from `spec.agents` deletes its Deployment and Jenkins node.

## Persistence

Node registration is stored in `$JENKINS_HOME`. With the default `emptyDir`
`$JENKINS_HOME`, the operator re-registers agents after a master pod restart.
For durable registration across restarts, use a PVC-backed home via
`spec.master.persistentVolumeClaim`. Agent **pods** are durable regardless, as
they are Kubernetes Deployments.
```

- [ ] **Step 6: Full build and test**

Run: `go build ./... && go test ./...`
Expected: exit 0, all PASS.

- [ ] **Step 7: Commit**

```bash
git add config/crd/bases/jenkins.io_jenkins.yaml deploy/crds/jenkins.io_jenkins_crd.yaml chart/jenkins-operator/crds/jenkins-crd.yaml docs/getting-started/latest/managing-agents.md
git commit -m "chore: regenerate CRDs and document persistent agents"
```

---

## Notes for the implementer

- If `go run /tmp/yamlcheck.go` complains about `sigs.k8s.io/yaml` not being importable from a temp path, run it from the repo root (it uses the repo's module and vendored deps).
- If the awk splice in Task 6 inserts at the wrong `annotations:` (verify with `grep -n "^                  agents:"` — it should appear once, before the first `annotations:` at 18-space indent, inside the `master` schema), re-run after restoring the file from git and adjust the anchor.
- Do not hand-edit `zz_generated.deepcopy.go`; always regenerate.
