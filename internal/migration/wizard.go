package migration

type Wizard struct{}

func NewWizard() *Wizard { return &Wizard{} }

func (w *Wizard) Plan(sourceType, sourceConfig map[string]string) (map[string]interface{}, error) {
	return map[string]interface{}{
		"domains":        0,
		"mailboxes":      0,
		"total_size_mb":  0,
		"estimated_time": "unknown",
	}, nil
}

func (w *Wizard) Start(sourceType string, config map[string]string, progressCh chan<- Progress) error {
	return nil
}
