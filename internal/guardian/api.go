package guardian

import "fmt"

var ErrEnterpriseOnly = fmt.Errorf("guardian API requires Enterprise license")

type API struct {
	agent *Agent
}

func NewAPI(agent *Agent) *API {
	return &API{agent: agent}
}

func (a *API) Analyze(content, sourceIP, senderDomain string) (*AnalysisResult, error) {
	return a.agent.Analyze(content, sourceIP, senderDomain)
}
