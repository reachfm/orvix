package guardian

import "sync"

type PatternLearner struct {
	mu       sync.Mutex
	patterns []ThreatPattern
}

type ThreatPattern struct {
	ID             string                 `json:"id"`
	Category       string                 `json:"category"`
	Indicators     map[string]interface{} `json:"indicators"`
	Count          int                    `json:"count"`
	FalsePositives int                    `json:"false_positives"`
}

func NewPatternLearner() *PatternLearner {
	return &PatternLearner{}
}

func (l *PatternLearner) Learn(threat map[string]interface{}) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	cat, _ := threat["category"].(string)
	if cat == "" {
		cat = "unknown"
	}

	for i, p := range l.patterns {
		if p.Category == cat {
			l.patterns[i].Count++
			return nil
		}
	}

	l.patterns = append(l.patterns, ThreatPattern{
		ID:         cat,
		Category:   cat,
		Indicators: threat,
		Count:      1,
	})

	return nil
}

func (l *PatternLearner) Match(features map[string]interface{}) (string, float64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, p := range l.patterns {
		matched := 0
		total := 0
		for k, v := range p.Indicators {
			total++
			if fv, ok := features[k]; ok && fv == v {
				matched++
			}
		}
		if total > 0 && float64(matched)/float64(total) > 0.7 {
			return p.Category, float64(matched) / float64(total)
		}
	}

	return "unknown", 0
}

func (l *PatternLearner) Patterns() []ThreatPattern {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]ThreatPattern, len(l.patterns))
	copy(result, l.patterns)
	return result
}
