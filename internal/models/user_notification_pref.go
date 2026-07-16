package models

type UserNotificationPreference struct {
	Common
	UserID             uint `gorm:"uniqueIndex;not null" json:"user_id"`
	DomainVerification bool `gorm:"not null;default:true" json:"domain_verification"`
	QuotaWarning       bool `gorm:"not null;default:true" json:"quota_warning"`
	QuotaReached       bool `gorm:"not null;default:true" json:"quota_reached"`
	BillingStatus      bool `gorm:"not null;default:true" json:"billing_status"`
	Invitation         bool `gorm:"not null;default:true" json:"invitation"`
	SessionActivity    bool `gorm:"not null;default:true" json:"session_activity"`
	ChannelEmail       bool `gorm:"not null;default:true" json:"channel_email"`
}
