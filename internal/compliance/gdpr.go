package compliance

type GDPRService struct{}
type LegalHoldService struct{}
type EDiscoveryService struct{}

func NewGDPRService() *GDPRService             { return &GDPRService{} }
func NewLegalHoldService() *LegalHoldService   { return &LegalHoldService{} }
func NewEDiscoveryService() *EDiscoveryService { return &EDiscoveryService{} }
