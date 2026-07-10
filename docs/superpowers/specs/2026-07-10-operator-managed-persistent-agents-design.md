# Operator-Managed Persistent Agents — Design

**Date:** 2026-07-10
**Status:** Approved (pending spec review)

## Problem

The operator only manages a single, hardcoded **seed-job agent** (`pkg/configuration/user/seedjobs/seedjobs.go`). There is no way to declare additional long-lived (persistent) inbound (JNLP) agents through the `Jenkins` CR. Users who need a fixed agent (special tooling, licensed software, dedicated hardware) currently must either use the Kubernetes cloud plugin (dynamic pods) or wire up agents by hand.

This feature generalizes the seed-job agent pattern into a declarative `spec.agents[]` field so the operator reconciles arbitrary persistent inbound agents.

## Goals

- Declare persistent inbound agents in the `Jenkins` CR.
- Operator registers each as a Jenkins node and runs it as a Kubernetes `Deployment`.
- Add/update/remove agents reconciled from spec, including pruning removed ones.

## Non-Goals

- Dynamic/autoscaling agents (that is what the Kubernetes cloud plugin + CasC is for).
- Full `PodSpec` flexibility per agent (custom volumes, multi-container, arbitrary env). Deferred; "Standard" surface only.
- Replacing or changing the existing seed-job agent behavior.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Config surface | **Standard** | name, image, replicas, numExecutors, label, resources, nodeSelector, tolerations. Covers most needs without a PodSpec-sized CRD. |
| Node registration | **Imperative Jenkins API** | Mirrors seed-job (`CreateNode`), self-contained, no co-editing user CasC. |
| Replicas type | **`*int32`, Minimum=1, Maximum=255** | Apiserver-enforced cap with clean rejection; no unsigned-int decode failures; matches `Deployment.Spec.Replicas`. |
| Deployment name | **`<jenkinsName>-<agentName>`** | Per user preference. Seed-job keeps its existing `<agentName>-<jenkinsName>`. |
| Deployment builder | **Shared/refactored** with seed-job | Avoid ~80 lines of drift-prone duplication; seed-job behavior preserved byte-for-byte. |
| Reconcile gating | **Do not block on readiness** | A bad user image must not stall the whole reconcile loop. |

## API

New `Agent` type; new `Agents []Agent` field on `JenkinsSpec` (`api/v1alpha2/jenkins_types.go`).

```go
type Agent struct {
    // Name is the unique agent name. Used as the Jenkins node name and the basis
    // for the agent Deployment name (<jenkinsName>-<name>). Must be a DNS label.
    Name string `json:"name"`

    // Image is the inbound agent image. Defaults to the operator's pinned
    // jenkins/inbound-agent image.
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
    // Defaults to Name.
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

### Example

```yaml
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
      nodeSelector:
        kubernetes.io/arch: amd64
```

## Architecture

New package `pkg/configuration/user/agents` mirroring `seedjobs`:

- `agents.go` — `Agents` struct, `New(...)`, `EnsureAgents(jenkins)`.
- `validate.go` — spec validation.
- `agents_test.go` — unit tests.

### Shared Deployment builder

Extract the seed-job `agentDeployment` (`seedjobs.go:414-520`) into a parameterized builder callable by both packages. Parameters: name, image, replicas, numExecutors, label (unused in Deployment, used for node), resources, nodeSelector, tolerations, jnlp secret, jenkins CR. Seed-job calls it with its current fixed values (replicas nil→1, no resources) so its output is unchanged and its existing tests keep passing.

Connection wiring (unchanged from seed-job): container `jnlp`; env `JENKINS_URL` (http service FQDN), `JENKINS_SECRET`, `JENKINS_AGENT_NAME`, `JENKINS_WEB_SOCKET=true`, `JENKINS_AGENT_WORKDIR`; `home` + `workspace` emptyDir volumes; `OwnerReference` to the Jenkins CR for GC.

Proposed shared location: a function in the `agents` package (e.g. `AgentDeployment(...)`) that seed-job imports, or a shared helper under `pkg/configuration/base/resources`. Final placement decided during implementation to avoid an import cycle; the constraint is that seed-job and agents both depend on it, not on each other.

### `EnsureAgents` flow (per reconcile)

1. For each `agent` in `spec.agents`:
   a. `GetNode(name)`; if "No node found" → `CreateNode(name, numExecutors, "operator-managed agent", "/home/jenkins", label)`.
   b. `GetNodeSecret(name)` to obtain the JNLP secret.
   c. Build the Deployment (shared builder) and `Create`, falling back to `Update` on AlreadyExists.
2. **Prune:** list Deployments carrying the operator-managed agent labels for this Jenkins CR; for any whose agent name is not in `spec.agents`, delete the Deployment and best-effort `DeleteNode(name)` (ignore "No node found"). Empty `spec.agents` ⇒ all pruned.

Naming/labels:
- Deployment name: `<jenkinsName>-<agentName>`.
- Labels for selection/pruning: `app.kubernetes.io/managed-by: jenkins-operator`, `jenkins.io/agent-cr: <jenkinsName>`, `jenkins.io/agent-name: <agentName>`.

### Reconcile hook

Add `ensureAgents()` immediately after `ensureSeedJobs()` in `reconcile.go` `ReconcileOthers` (`reconcile.go:71-77`), same error/requeue shape.

## Validation (`agents/validate.go`)

- `Name` non-empty and a valid DNS label.
- `Name` unique across `spec.agents`.
- `Name` must not equal the reserved `seed-job-agent`.
- `NumExecutors >= 1` when set (default 1).
- Replicas bounds enforced by the CRD schema (Minimum=1, Maximum=255).

Surfaced through the existing validation-messages mechanism used by `Validate`.

## Persistence

Node registration is imperative and stored by Jenkins in `$JENKINS_HOME/nodes/<name>/config.xml`. With the default emptyDir `$JENKINS_HOME`, this is re-asserted each reconcile after a master restart, and the JNLP secret churns (reconnect gap). For **true** persistence across master restarts, use PVC-backed `$JENKINS_HOME` (`spec.master.persistentVolumeClaim`). The agent **pods** (Deployments) are durable regardless. This will be documented on the `spec.agents` field and in the feature docs.

## Testing

- Unit tests for the shared Deployment builder: image default, replicas default (1) and set value, numExecutors, label default (= name), resources, nodeSelector, tolerations, env vars, owner reference, Deployment name format.
- Unit tests for validation: empty name, duplicate names, reserved name collision, executors bounds.
- Regenerate deepcopy + CRD; sync CRD to `deploy/crds` and `chart/jenkins-operator/crds` (same as the PVC change).

## Rollout / Compatibility

- Purely additive: `spec.agents` is optional; existing CRs unaffected.
- Seed-job agent behavior unchanged.
- No new RBAC required: the operator already creates/updates/deletes agent Deployments for the seed-job agent; static inbound agents connect over JNLP and do not require the Jenkins master SA to create pods.
