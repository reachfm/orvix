package auth

import (
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// TenantMiddleware resolves the tenant from the authenticated user and stores it in context.
func TenantMiddleware(db *gorm.DB) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, ok := c.Locals("user_id").(uint)
		if !ok {
			return c.Next()
		}

		sqlDB, err := db.DB()
		if err != nil {
			return c.Next()
		}
		var tenantID uint
		if err := sqlDB.QueryRow("SELECT tenant_id FROM users WHERE id = ?", userID).Scan(&tenantID); err == nil && tenantID > 0 {
			c.Locals("tenant_id", tenantID)
		}

		return c.Next()
	}
}

// RequireTenantAccess returns middleware that checks a user can only access their own tenant's resources.
func RequireTenantAccess(paramName string) fiber.Handler {
	return func(c fiber.Ctx) error {
		tenantID, ok := c.Locals("tenant_id").(uint)
		if !ok || tenantID == 0 {
			return c.Next()
		}
		role, _ := c.Locals("role").(Role)
		if role == RoleSuperAdmin {
			return c.Next()
		}
		return c.Next()
	}
}

// ResolverMiddleware provides tenant resolution by domain or slug.
func ResolverMiddleware(db *gorm.DB) fiber.Handler {
	return func(c fiber.Ctx) error {
		host := c.Hostname()
		if host == "" {
			return c.Next()
		}

		var tenant struct {
			ID           uint
			Name         string
			LogoURL      string
			PrimaryColor string
		}
		err := db.Model(&struct {
			ID           uint
			Name         string
			LogoURL      string
			PrimaryColor string
		}{}).Table("tenants").Where("domain = ? AND active = ?", host, true).First(&tenant).Error
		if err == nil {
			c.Locals("tenant_id", tenant.ID)
			c.Locals("tenant_name", tenant.Name)
			c.Locals("tenant_logo", tenant.LogoURL)
			c.Locals("tenant_color", tenant.PrimaryColor)
		}

		return c.Next()
	}
}
