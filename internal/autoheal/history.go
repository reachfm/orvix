package autoheal

import "time"

type HealEntry struct {
	ID          uint      `json:"id"`
	CheckName   string    `json:"check_name"`
	Severity    string    `json:"severity"`
	Issue       string    `json:"issue"`
	BeforeState string    `json:"before_state"`
	AfterState  string    `json:"after_state"`
	AutoFixed   bool      `json:"auto_fixed"`
	Success     bool      `json:"success"`
	PerformedAt time.Time `json:"performed_at"`
}

type History struct {
	entries []HealEntry
}

func NewHistory() *History {
	return &History{}
}

func (h *History) Log(entry HealEntry) {
	h.entries = append(h.entries, entry)
}

func (h *History) GetAll() []HealEntry {
	return h.entries
}

func (h *History) GetByCheck(name string) []HealEntry {
	var filtered []HealEntry
	for _, e := range h.entries {
		if e.CheckName == name {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
