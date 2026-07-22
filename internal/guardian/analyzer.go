package guardian

import (
	"math"
	"strings"
)

type Analyzer struct {
	patterns *PatternLearner
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{
		patterns: NewPatternLearner(),
	}
}

func (a *Analyzer) BuildFeatureVector(raw map[string]interface{}) map[string]interface{} {
	vector := make(map[string]interface{})

	if ip, ok := raw["sender_ip"].(string); ok {
		vector["ip"] = ip
	}
	if domain, ok := raw["sender_domain"].(string); ok {
		vector["domain"] = domain
		vector["domain_age_days"] = 0
		vector["has_mx_record"] = true
	}
	if subject, ok := raw["subject"].(string); ok {
		vector["subject_length"] = len(subject)
		vector["has_urgency"] = containsAny(subject, []string{"urgent", "immediately", "action required", "suspended", "verified", "account", "wire", "payment"})
		vector["has_money"] = containsAny(subject, []string{"$", "money", "transfer", "bank", "credit", "invoice"})
	}
	if _, ok := raw["has_attachments"].(bool); ok {
		vector["has_attachments"] = raw["has_attachments"]
	}
	if types, ok := raw["attachment_types"].([]string); ok {
		vector["dangerous_attachment"] = false
		for _, t := range types {
			ext := strings.ToLower(t)
			if ext == ".exe" || ext == ".bat" || ext == ".ps1" || ext == ".vbs" || ext == ".scr" || ext == ".js" {
				vector["dangerous_attachment"] = true
				break
			}
		}
	}
	if spf, ok := raw["spf_result"].(string); ok {
		vector["spf_fail"] = spf == "fail" || spf == "hardfail"
	}
	if dkim, ok := raw["dkim_result"].(string); ok {
		vector["dkim_fail"] = dkim == "fail"
	}

	return vector
}

func (a *Analyzer) Score(features map[string]interface{}) (float64, error) {
	score := 0.0

	if hasUrgency, ok := features["has_urgency"].(bool); ok && hasUrgency {
		score += 15
	}
	if hasMoney, ok := features["has_money"].(bool); ok && hasMoney {
		score += 10
	}
	if dangerous, ok := features["dangerous_attachment"].(bool); ok && dangerous {
		score += 30
	}
	if spfFail, ok := features["spf_fail"].(bool); ok && spfFail {
		score += 25
	}
	if dkimFail, ok := features["dkim_fail"].(bool); ok && dkimFail {
		score += 15
	}
	if subjectLen, ok := features["subject_length"].(float64); ok && subjectLen == 0 {
		score += 5
	}

	score = math.Min(score, 100)

	return score, nil
}

func containsAny(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}
