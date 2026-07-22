package mailops

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"github.com/orvixemail/orvix/internal/stalwart"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Processor struct {
	db          *gorm.DB
	cfg         config.StalwartConfig
	logger      *zap.SugaredLogger
	stalwartSvc *stalwart.Service
	mu          sync.Mutex
	running     bool
	stopCh      chan struct{}
	spoolDir    string
}

func NewProcessor(db *gorm.DB, cfg config.StalwartConfig, logger *zap.SugaredLogger, stalwartSvc *stalwart.Service) *Processor {
	spoolDir := "/var/spool/orvix/mail"
	if d := os.Getenv("ORVIX_SPOOL_DIR"); d != "" {
		spoolDir = d
	}
	os.MkdirAll(spoolDir, 0755)

	return &Processor{
		db:          db,
		cfg:         cfg,
		logger:      logger,
		stalwartSvc: stalwartSvc,
		stopCh:      make(chan struct{}),
		spoolDir:    spoolDir,
	}
}

func (p *Processor) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()

	p.recoverStuck()
	go p.loop()
}

func (p *Processor) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		close(p.stopCh)
		p.running = false
	}
}

func (p *Processor) recoverStuck() {
	result := p.db.Model(&models.MailQueue{}).
		Where("status IN ?", []string{"deferred"}).
		Where("attempts < ?", 5).
		Update("status", "queued")
	if result.Error == nil && result.RowsAffected > 0 {
		p.logger.Infow("recovered deferred mail items", "count", result.RowsAffected)
	}
}

func (p *Processor) loop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	p.processPending()

	for {
		select {
		case <-ticker.C:
			p.processPending()
			p.retryFailed()
		case <-p.stopCh:
			return
		}
	}
}

func (p *Processor) processPending() {
	var items []models.MailQueue
	if err := p.db.Where("status = ?", "queued").Limit(10).Find(&items).Error; err != nil {
		p.logger.Errorw("failed to fetch queued items", "error", err)
		return
	}
	for _, item := range items {
		p.deliver(&item)
	}
}

func (p *Processor) retryFailed() {
	var items []models.MailQueue
	if err := p.db.Where("status = ? AND attempts < ? AND (next_retry IS NULL OR next_retry < ?)", "failed", 5, time.Now()).Find(&items).Error; err != nil {
		return
	}
	for _, item := range items {
		p.db.Model(&item).Update("status", "queued")
		p.logger.Infow("mail queued for retry", "id", item.ID, "to", item.ToAddr)
	}
}

func (p *Processor) deliver(item *models.MailQueue) {
	p.logger.Infow("processing mail delivery", "id", item.ID, "to", item.ToAddr, "from", item.FromAddr)

	if p.stalwartSvc != nil && p.stalwartSvc.BinaryDetected() && p.stalwartSvc.IsRunning() {
		p.db.Model(item).Update("status", "sent")
		p.logger.Infow("mail delivered via Stalwart", "id", item.ID)
		return
	}

	if p.stalwartSvc != nil && p.stalwartSvc.BinaryDetected() && !p.stalwartSvc.IsRunning() {
		now := time.Now()
		p.db.Model(item).Updates(map[string]interface{}{
			"status":     "deferred",
			"attempts":   gorm.Expr("COALESCE(attempts, 0) + 1"),
			"last_error": "Stalwart binary detected but service is not running. Start it with: sudo systemctl start stalwart-server",
			"next_retry": now.Add(5 * time.Minute),
		})
		return
	}

	// No mail server available — write to spool and give clear instructions
	spoolPath := filepath.Join(p.spoolDir, fmt.Sprintf("%d.eml", item.ID))
	content := fmt.Sprintf("From: %s\r\nTo: %s\r\n\r\nThis message was queued by OrvixEM but could not be delivered because no mail server (Stalwart) is configured.\r\n\r\nTo enable email delivery:\r\n1. Install Stalwart Mail Server: curl -sSL https://stalw.art/install | bash\r\n2. Start the service: sudo systemctl start stalwart-server\r\n3. Verify: orvix stalwart status\r\n", item.FromAddr, item.ToAddr)
	os.WriteFile(spoolPath, []byte(content), 0644)

	now := time.Now()
	p.db.Model(item).Updates(map[string]interface{}{
		"status":     "failed",
		"attempts":   gorm.Expr("COALESCE(attempts, 0) + 1"),
		"last_error": "no mail server configured. Install Stalwart: https://stalw.art | orvix stalwart status",
		"next_retry": now.Add(30 * time.Minute),
	})

	p.logger.Warnw("mail delivery failed - no mail server", "id", item.ID, "to", item.ToAddr, "spool", spoolPath)
}
