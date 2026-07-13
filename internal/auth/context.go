package auth

import "github.com/gofiber/fiber/v3"

type AuthenticatedActor struct {
	UserID   uint
	Role     Role
	TenantID uint
	Email    string
}

func ActorFromCtx(c fiber.Ctx) (AuthenticatedActor, bool) {
	userID, ok := c.Locals("user_id").(uint)
	if !ok || userID == 0 {
		return AuthenticatedActor{}, false
	}
	role, _ := c.Locals("role").(Role)
	tenantID, _ := c.Locals("tenant_id").(uint)
	email, _ := c.Locals("email").(string)
	return AuthenticatedActor{
		UserID:   userID,
		Role:     role,
		TenantID: tenantID,
		Email:    email,
	}, true
}

func RequireTenantID(c fiber.Ctx) (uint, error) {
	actor, ok := ActorFromCtx(c)
	if !ok {
		return 0, fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	if actor.TenantID == 0 {
		return 0, fiber.NewError(fiber.StatusForbidden, "tenant context required")
	}
	return actor.TenantID, nil
}

func RequireActorTenantID(c fiber.Ctx) (AuthenticatedActor, error) {
	actor, ok := ActorFromCtx(c)
	if !ok {
		return AuthenticatedActor{}, fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	if actor.TenantID == 0 {
		return AuthenticatedActor{}, fiber.NewError(fiber.StatusForbidden, "tenant context required")
	}
	return actor, nil
}

func ActorTenantID(c fiber.Ctx) (uint, uint, bool) {
	userID, ok := c.Locals("user_id").(uint)
	if !ok || userID == 0 {
		return 0, 0, false
	}
	tenantID, _ := c.Locals("tenant_id").(uint)
	return userID, tenantID, true
}
