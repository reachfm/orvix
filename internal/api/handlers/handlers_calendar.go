package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/calendar"
)

func (h *Handler) ListEvents(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var events []calendar.Event
	h.db.Where("user_id = ?", userID).Order("start_time asc").Find(&events)
	return c.JSON(events)
}

func (h *Handler) CreateEvent(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		StartTime   time.Time `json:"start_time"`
		EndTime     time.Time `json:"end_time"`
		AllDay      bool      `json:"all_day"`
		Location    string    `json:"location"`
		Color       string    `json:"color"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	event := calendar.Event{
		UserID: userID, Title: req.Title, Description: req.Description,
		StartTime: req.StartTime, EndTime: req.EndTime,
		AllDay: req.AllDay, Location: req.Location, Color: req.Color,
	}
	if err := h.db.Create(&event).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create event"})
	}
	return c.Status(201).JSON(event)
}

func (h *Handler) UpdateEvent(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req calendar.Event
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Model(&calendar.Event{}).Where("id = ? AND user_id = ?", c.Params("id"), userID).Updates(&req)
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteEvent(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	h.db.Where("id = ? AND user_id = ?", c.Params("id"), userID).Delete(&calendar.Event{})
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListContacts(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var contacts []calendar.Contact
	h.db.Where("user_id = ?", userID).Order("name asc").Find(&contacts)
	return c.JSON(contacts)
}

func (h *Handler) CreateContact(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req calendar.Contact
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	req.UserID = userID
	if err := h.db.Create(&req).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create contact"})
	}
	return c.Status(201).JSON(req)
}

func (h *Handler) UpdateContact(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req calendar.Contact
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Model(&calendar.Contact{}).Where("id = ? AND user_id = ?", c.Params("id"), userID).Updates(&req)
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteContact(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	h.db.Where("id = ? AND user_id = ?", c.Params("id"), userID).Delete(&calendar.Contact{})
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListTasks(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var tasks []calendar.Task
	h.db.Where("user_id = ?", userID).Order("created_at desc").Find(&tasks)
	return c.JSON(tasks)
}

func (h *Handler) CreateTask(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req calendar.Task
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	req.UserID = userID
	if err := h.db.Create(&req).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create task"})
	}
	return c.Status(201).JSON(req)
}

func (h *Handler) UpdateTask(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req calendar.Task
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Model(&calendar.Task{}).Where("id = ? AND user_id = ?", c.Params("id"), userID).Updates(&req)
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) CompleteTask(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	h.db.Model(&calendar.Task{}).Where("id = ? AND user_id = ?", c.Params("id"), userID).Update("completed", true)
	return c.JSON(fiber.Map{"status": "completed"})
}

func (h *Handler) DeleteTask(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	h.db.Where("id = ? AND user_id = ?", c.Params("id"), userID).Delete(&calendar.Task{})
	return c.JSON(fiber.Map{"status": "deleted"})
}
