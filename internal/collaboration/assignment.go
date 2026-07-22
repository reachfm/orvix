package collaboration

type Assignment struct {
	EmailID    string `json:"email_id"`
	AssigneeID uint   `json:"assignee_id"`
	AssignedBy uint   `json:"assigned_by"`
	Status     string `json:"status"`
}

type AssignmentService struct{}

func NewAssignmentService() *AssignmentService { return &AssignmentService{} }

func (s *AssignmentService) Assign(emailID string, userID, assignedBy uint) error {
	return nil
}

func (s *AssignmentService) Unassign(emailID string) error {
	return nil
}

func (s *AssignmentService) GetAssignment(emailID string) (*Assignment, error) {
	return nil, nil
}
