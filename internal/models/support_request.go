package models

import "time"

type SupportRequest struct {
	ID              uint      `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ReferenceID     string    `gorm:"uniqueIndex;not null" json:"reference_id"`
	TenantID        uint      `gorm:"index;not null" json:"tenant_id"`
	UserID          uint      `gorm:"index;not null" json:"user_id"`
	UserEmail       string    `gorm:"not null" json:"user_email"`
	Category        string    `gorm:"not null" json:"category"`
	Subject         string    `gorm:"not null" json:"subject"`
	Message         string    `gorm:"not null;type:text" json:"message"`
	Status          string    `gorm:"not null;default:'received'" json:"status"`
	DeliveryStatus  string    `gorm:"not null;default:'pending'" json:"delivery_status"`
	DeliveryError   string    `gorm:"type:text" json:"delivery_error,omitempty"`
}
