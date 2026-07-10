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
				Master: v1alpha2.JenkinsMaster{
					Containers: []v1alpha2.Container{{Name: "jenkins-master"}},
				},
				Agents: []v1alpha2.Agent{{Name: "build-agent", NumExecutors: 2}},
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
