package calendar

import (
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Calendar/Contacts/Tasks.
type Module struct {
	cfg    *config.Config
	db     *gorm.DB
	logger *zap.Logger
}

func (m *Module) ID() string { return "calendar" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	_ = db.AutoMigrate(&Event{}, &Contact{}, &Task{})
	m.logger.Info("calendar module initialized")
	return nil
}

func (m *Module) Start() error { m.logger.Info("calendar module started"); return nil }
func (m *Module) Stop() error { m.logger.Info("calendar module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

// Event represents a calendar event.
type Event struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"index;not null" json:"user_id"`
	Title       string    `gorm:"not null" json:"title"`
	Description string    `gorm:"type:text" json:"description"`
	StartTime   time.Time `gorm:"not null" json:"start_time"`
	EndTime     time.Time `gorm:"not null" json:"end_time"`
	AllDay      bool      `gorm:"default:false" json:"all_day"`
	Location    string    `json:"location"`
	Color       string    `json:"color"`
	Recurrence  string    `gorm:"type:text" json:"recurrence"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Contact represents a contact.
type Contact struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"index;not null" json:"user_id"`
	Name        string    `gorm:"not null" json:"name"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone"`
	Company     string    `json:"company"`
	Notes       string    `gorm:"type:text" json:"notes"`
	PhotoURL    string    `json:"photo_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Task represents a task/todo item.
type Task struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"index;not null" json:"user_id"`
	Title       string    `gorm:"not null" json:"title"`
	Description string    `gorm:"type:text" json:"description"`
	DueDate     *time.Time `json:"due_date"`
	Completed   bool      `gorm:"default:false" json:"completed"`
	Priority    string    `gorm:"default:'medium'" json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var _ modules.Module = (*Module)(nil)
