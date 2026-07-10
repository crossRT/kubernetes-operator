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
