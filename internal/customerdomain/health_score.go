package customerdomain

// HealthScore computes a deterministic 0-100 score from a DNS inspection result.
// The score uses only the explicit DNS-check result and produces the same result
// for the same inputs — no network or database access.
//
// Component weights:
//
//	MX:   30
//	SPF:  20
//	DKIM: 30
//	DMARC: 20
func HealthScore(r *DNSResult) HealthScoreResult {
	result := HealthScoreResult{
		Breakdown: make(map[string]ScoreComponent),
	}

	add := func(name string, weight int, status string) {
		earned := 0
		switch status {
		case "pass":
			earned = weight
		case "warning":
			earned = weight / 2
		}
		result.Breakdown[name] = ScoreComponent{Weight: weight, Earned: earned, Status: status}
		result.Score += earned
	}

	mx := "unknown"
	if r != nil && r.MX != nil {
		mx = r.MX.Status
	}
	spf := "unknown"
	if r != nil && r.SPF != nil {
		spf = r.SPF.Status
	}
	dkim := "unknown"
	if r != nil && r.DKIM != nil {
		dkim = r.DKIM.Status
	}
	dmarc := "unknown"
	if r != nil && r.DMARC != nil {
		dmarc = r.DMARC.Status
	}

	add("mx", 30, mx)
	add("spf", 20, spf)
	add("dkim", 30, dkim)
	add("dmarc", 20, dmarc)

	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 100 {
		result.Score = 100
	}
	return result
}

// HealthScoreResult is the deterministic scoring output.
type HealthScoreResult struct {
	Score     int                       `json:"score"`
	Breakdown map[string]ScoreComponent `json:"breakdown"`
}

// ScoreComponent represents one component's contribution.
type ScoreComponent struct {
	Weight int    `json:"weight"`
	Earned int    `json:"earned"`
	Status string `json:"status"`
}
