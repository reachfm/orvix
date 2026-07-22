package provision

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupProvisionDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.ProvisioningJob{}, &models.Domain{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestRecoverStuckJobs(t *testing.T) {
	db := setupProvisionDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Create stuck jobs
	jobs := []models.ProvisioningJob{
		{DomainID: 1, DomainName: "example.com", Type: "provision", Status: "running"},
		{DomainID: 2, DomainName: "test.org", Type: "provision", Status: "running"},
	}
	for _, j := range jobs {
		db.Create(&j)
	}

	runner := NewJobRunner(db, config.StalwartConfig{}, sugar, nil)
	runner.recoverStuck()

	var pending []models.ProvisioningJob
	db.Where("status = ?", "pending").Find(&pending)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending jobs, got %d", len(pending))
	}
}

func TestEnqueueJob(t *testing.T) {
	db := setupProvisionDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	runner := NewJobRunner(db, config.StalwartConfig{}, sugar, nil)

	job, err := runner.Enqueue(1, "example.com")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if job.DomainName != "example.com" {
		t.Errorf("expected domain_name=example.com, got %s", job.DomainName)
	}
	if job.Status != "pending" {
		t.Errorf("expected status=pending, got %s", job.Status)
	}

	var count int64
	db.Model(&models.ProvisioningJob{}).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 job in DB, got %d", count)
	}
}
