package provision

import (
	"fmt"
	"sync"
	"time"

	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"github.com/orvixemail/orvix/internal/stalwart"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type JobRunner struct {
	db          *gorm.DB
	cfg         config.StalwartConfig
	logger      *zap.SugaredLogger
	stalwartSvc *stalwart.Service
	mu          sync.Mutex
	running     bool
	stopCh      chan struct{}
}

func NewJobRunner(db *gorm.DB, cfg config.StalwartConfig, logger *zap.SugaredLogger, stalwartSvc *stalwart.Service) *JobRunner {
	return &JobRunner{
		db:          db,
		cfg:         cfg,
		logger:      logger,
		stalwartSvc: stalwartSvc,
		stopCh:      make(chan struct{}),
	}
}

func (r *JobRunner) Start() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	// Recover jobs stuck in "running" state after restart
	r.recoverStuck()

	go r.loop()
}

func (r *JobRunner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		close(r.stopCh)
		r.running = false
	}
}

func (r *JobRunner) recoverStuck() {
	result := r.db.Model(&models.ProvisioningJob{}).
		Where("status = ?", "running").
		Update("status", "pending")
	if result.Error == nil && result.RowsAffected > 0 {
		r.logger.Infow("recovered stuck provisioning jobs", "count", result.RowsAffected)
	}
}

func (r *JobRunner) loop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	r.processPending()

	for {
		select {
		case <-ticker.C:
			r.processPending()
		case <-r.stopCh:
			return
		}
	}
}

func (r *JobRunner) processPending() {
	var jobs []models.ProvisioningJob
	if err := r.db.Where("status = ?", "pending").Find(&jobs).Error; err != nil {
		r.logger.Errorw("failed to fetch pending jobs", "error", err)
		return
	}

	for _, job := range jobs {
		r.processJob(&job)
	}
}

func (r *JobRunner) processJob(job *models.ProvisioningJob) {
	r.logger.Infow("processing provisioning job", "id", job.ID, "domain", job.DomainName)

	now := time.Now()
	r.db.Model(job).Updates(map[string]interface{}{
		"status":     "running",
		"started_at": &now,
	})

	stalwartProv := stalwart.NewProvisioningService(r.cfg, r.logger, r.stalwartSvc)

	stalwartOK := false
	if stalwartProv.IsAvailable() {
		if err := stalwartProv.CreateDomain(job.DomainName); err != nil {
			r.logger.Errorw("stalwart domain creation failed", "domain", job.DomainName, "error", err)
			r.db.Model(job).Update("stalwart_result", "failed")
			r.db.Model(job).Update("error_message", err.Error())
		} else {
			stalwartOK = true
			r.db.Model(job).Update("stalwart_result", "ok")
		}
	} else {
		r.db.Model(job).Update("stalwart_result", "skipped")
	}

	r.db.Model(job).Update("dns_setup_status", "manual")

	completedAt := time.Now()
	status := "completed"
	if !stalwartOK && stalwartProv.IsAvailable() {
		status = "failed"
	}

	r.db.Model(job).Updates(map[string]interface{}{
		"status":       status,
		"completed_at": &completedAt,
	})

	if status == "completed" {
		r.db.Model(&models.Domain{}).Where("id = ?", job.DomainID).Update("status", "active")
	}

	r.logger.Infow("provisioning job completed", "id", job.ID, "domain", job.DomainName, "status", status)
}

func (r *JobRunner) Enqueue(domainID uint, domainName string) (*models.ProvisioningJob, error) {
	job := &models.ProvisioningJob{
		DomainID:       domainID,
		DomainName:     domainName,
		Type:           "provision",
		Status:         "pending",
		StalwartResult: "pending",
		DNSSetupStatus: "pending",
	}
	if err := r.db.Create(job).Error; err != nil {
		return nil, fmt.Errorf("failed to enqueue provisioning job: %w", err)
	}
	r.logger.Infow("provisioning job enqueued", "id", job.ID, "domain", domainName)
	return job, nil
}
