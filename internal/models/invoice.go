package models

import "time"

type Invoice struct {
	ID                uint       `json:"id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	TenantID          uint       `gorm:"index;not null" json:"tenant_id"`
	SubscriptionID    uint       `gorm:"index" json:"subscription_id"`
	Provider          string     `gorm:"not null;default:''" json:"provider"`
	ProviderInvoiceID string     `gorm:"uniqueIndex" json:"provider_invoice_id"`
	InvoiceNumber     string     `gorm:"index" json:"invoice_number"`
	Currency          string     `gorm:"not null;default:'usd'" json:"currency"`
	Subtotal          int64      `gorm:"not null;default:0" json:"subtotal"`
	Tax               int64      `gorm:"not null;default:0" json:"tax"`
	Total             int64      `gorm:"not null;default:0" json:"total"`
	AmountPaid        int64      `gorm:"not null;default:0" json:"amount_paid"`
	AmountDue         int64      `gorm:"not null;default:0" json:"amount_due"`
	Status            string     `gorm:"not null;default:'draft'" json:"status"`
	PeriodStart       *time.Time `json:"period_start,omitempty"`
	PeriodEnd         *time.Time `json:"period_end,omitempty"`
	IssuedAt          *time.Time `json:"issued_at,omitempty"`
	DueAt             *time.Time `json:"due_at,omitempty"`
	PaidAt            *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL  string     `gorm:"type:text" json:"hosted_invoice_url,omitempty"`
	PDFURL            string     `gorm:"type:text" json:"pdf_url,omitempty"`
}
