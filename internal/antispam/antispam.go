package antispam

type PolicyEngine struct{}

func NewPolicyEngine() *PolicyEngine { return &PolicyEngine{} }

func (e *PolicyEngine) Score(domain string, features map[string]interface{}) (float64, error) {
	return 0, nil
}

func (e *PolicyEngine) Threshold(domain string) float64 {
	return 5.0
}
