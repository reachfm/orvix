package auth

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SecurityEvent represents a persisted security event.
type SecurityEvent struct {
	ID        uint      `gorm:"primaryKey"`
	IP        string    `gorm:"index;not null"`
	Email     string    `gorm:"index"`
	EventType string    `gorm:"not null"`
	Count     int       `gorm:"not null;default:1"`
	CreatedAt time.Time `gorm:"index"`
}

// SecurityMonitor tracks security events and generates alerts using database persistence.
type SecurityMonitor struct {
	db          *gorm.DB
	logger      *zap.Logger
	alertSender *AlertSender
}

// NewSecurityMonitor creates a new database-backed security monitor.
func NewSecurityMonitor(db *gorm.DB, logger *zap.Logger) *SecurityMonitor {
	_ = db.AutoMigrate(&SecurityEvent{})
	return &SecurityMonitor{
		db:     db,
		logger: logger,
	}
}

// SetAlertSender sets the alert sender for delivering notifications.
func (sm *SecurityMonitor) SetAlertSender(a *AlertSender) {
	sm.alertSender = a
}

// RecordFailedLogin records a failed login attempt and alerts if threshold exceeded.
func (sm *SecurityMonitor) RecordFailedLogin(ctx context.Context, ip, email string) {
	now := time.Now()

	event := SecurityEvent{
		IP:        ip,
		Email:     email,
		EventType: "failed_login",
		Count:     1,
		CreatedAt: now,
	}
	sm.db.Create(&event)

	cutoff := now.Add(-5 * time.Minute)
	var count int64
	sm.db.Model(&SecurityEvent{}).
		Where("ip = ? AND event_type = ? AND created_at > ?", ip, "failed_login", cutoff).
		Count(&count)

	sm.logger.Warn("failed login attempt",
		zap.String("ip", ip),
		zap.String("email", email),
		zap.Int64("recent_failures", count),
	)

	if count >= 5 {
		sm.alertAdmin(ctx, ip, email, int(count))
	}
}

// RecordSuccessfulLogin records a successful login and clears failed attempt history for the IP.
func (sm *SecurityMonitor) RecordSuccessfulLogin(ip string) {
	sm.db.Where("ip = ? AND event_type = ?", ip, "failed_login").Delete(&SecurityEvent{})
}

// alertAdmin persists a security alert and delivers it via AlertSender.
func (sm *SecurityMonitor) alertAdmin(ctx context.Context, ip, email string, count int) {
	alert := SecurityEvent{
		IP:        ip,
		Email:     email,
		EventType: "security_alert",
		Count:     count,
		CreatedAt: time.Now(),
	}
	sm.db.Create(&alert)

	sm.logger.Error("SECURITY ALERT: multiple failed logins",
		zap.String("ip", ip),
		zap.String("email", email),
		zap.Int("count", count),
		zap.String("window", "5 minutes"),
	)

	if sm.alertSender != nil {
		var tenantID uint
		sm.db.Table("users").Select("tenant_id").Where("email = ?", email).Scan(&tenantID)
		sm.alertSender.SendAlert(ctx, tenantID, AlertEvent{
			Type:     "failed_login",
			Severity: "high",
			Message:  fmt.Sprintf("%d failed login attempts from %s targeting %s", count, ip, email),
			IP:       ip,
			Email:    email,
		})
	}

	var adminEmails []string
	sm.db.Table("users").Where("role IN ?", []string{"superadmin", "admin"}).
		Pluck("email", &adminEmails)

	for _, admin := range adminEmails {
		message := fmt.Sprintf(
			"Security Alert: %d failed login attempts from IP %s targeting account %s in the last 5 minutes.",
			count, ip, email,
		)
		sm.logger.Warn("admin alert persisted",
			zap.String("admin", admin),
			zap.String("alert", message),
		)
	}
}
