package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/compose"
)

// ComposeComplete generates a full AI completion.
func (h *Handler) ComposeComplete(c fiber.Ctx) error {
	var req struct {
		Context string `json:"context"`
		Prompt  string `json:"prompt"`
		Tone    string `json:"tone"`
		Action  string `json:"action"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}

	for _, mod := range h.registry.All() {
		if mod.ID() == "smart-compose" {
			if s, ok := mod.(interface{ Streamer() *compose.Streamer }); ok {
				result, err := s.Streamer().Complete(c.Context(), &compose.CompletionRequest{
					Context: req.Context, Prompt: req.Prompt, Tone: req.Tone, Action: req.Action, MaxTokens: 500,
				})
				if err != nil {
					return c.Status(500).JSON(fiber.Map{"error": err.Error()})
				}
				return c.JSON(fiber.Map{"completion": result})
			}
		}
	}
	return c.JSON(fiber.Map{"completion": "Smart Compose module not available"})
}

// ComposeStream streams an AI completion.
func (h *Handler) ComposeStream(c fiber.Ctx) error {
	var req struct {
		Context string `json:"context"`
		Prompt  string `json:"prompt"`
		Tone    string `json:"tone"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	for _, mod := range h.registry.All() {
		if mod.ID() == "smart-compose" {
			if s, ok := mod.(interface{ Streamer() *compose.Streamer }); ok {
				s.Streamer().Stream(c.Context(), &compose.CompletionRequest{
					Context: req.Context, Prompt: req.Prompt, Tone: req.Tone, MaxTokens: 500,
				}, func(chunk string) {
					c.Write([]byte("data: " + chunk + "\n\n"))
				})
			}
		}
	}
	c.Write([]byte("data: [DONE]\n\n"))
	return nil
}
