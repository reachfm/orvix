package migration

type ProgressTracker struct {
	tasks map[string]*TaskProgress
}

type TaskProgress struct {
	Mailbox   string  `json:"mailbox"`
	Processed int64   `json:"processed"`
	Total     int64   `json:"total"`
	Percent   float64 `json:"percent"`
	Status    string  `json:"status"`
	Error     string  `json:"error,omitempty"`
}

func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{tasks: make(map[string]*TaskProgress)}
}

func (t *ProgressTracker) Update(mailbox string, processed, total int64) {
	tp, ok := t.tasks[mailbox]
	if !ok {
		tp = &TaskProgress{Mailbox: mailbox, Status: "running"}
		t.tasks[mailbox] = tp
	}
	tp.Processed = processed
	tp.Total = total
	if total > 0 {
		tp.Percent = float64(processed) / float64(total) * 100
	}
}

func (t *ProgressTracker) Complete(mailbox string) {
	if tp, ok := t.tasks[mailbox]; ok {
		tp.Status = "completed"
		tp.Percent = 100
	}
}

func (t *ProgressTracker) Fail(mailbox string, err error) {
	if tp, ok := t.tasks[mailbox]; ok {
		tp.Status = "failed"
		tp.Error = err.Error()
	}
}

func (t *ProgressTracker) Status() map[string]*TaskProgress {
	return t.tasks
}

func (t *ProgressTracker) Reset() {
	t.tasks = make(map[string]*TaskProgress)
}
