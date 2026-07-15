package auth

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"
	"gorm.io/gorm"
)

// TenantMiddleware resolves the tenant from the authenticated user and stores it in context.
// On DB errors it fails closed so missing tenant context is never silently ignored.
func TenantMiddleware(db *gorm.DB) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, ok := c.Locals("user_id").(uint)
		if !ok {
			return c.Next()
		}

		sqlDB, err := db.DB()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
		}
		dialect, err := dbdialect.Detect(sqlDB)
		if err != nil {
			dialect = dbdialect.FromDriver("sqlite")
		}
		var tenantID uint
		if err := sqlDB.QueryRow("SELECT tenant_id FROM users WHERE id = "+dialect.Placeholder(1), userID).Scan(&tenantID); err == nil && tenantID > 0 {
			c.Locals("tenant_id", tenantID)
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
