package provision

import "errors"

var (
	ErrISPTierRequired = errors.New("instant deploy API requires ISP+ tier")
	ErrProvisionFailed = errors.New("domain provisioning failed")
)

type DomainResponse struct {
	JobID    uint   `json:"job_id"`
	Domain   string `json:"domain"`
	Status   string `json:"status"`
	DNSSetup string `json:"dns_setup"`
}

type API struct {
	runner *JobRunner
}

func NewAPI(runner *JobRunner) *API {
	return &API{runner: runner}
}

func (a *API) DeployDomain(domainName string, domainID uint) (*DomainResponse, error) {
	if !a.runner.running {
		return nil, ErrProvisionFailed
	}

	job, err := a.runner.Enqueue(domainID, domainName)
	if err != nil {
		return nil, err
	}

	return &DomainResponse{
		JobID:    job.ID,
		Domain:   domainName,
		Status:   "provisioning",
		DNSSetup: "manual",
	}, nil
}
