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
