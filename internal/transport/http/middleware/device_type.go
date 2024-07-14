package middleware

import (
	"slices"

	"github.com/gofiber/fiber/v2"
)

// RequireDeviceType allows only devices of the given types through.
// Must run after DeviceAuth.
//
// This keeps each screen to its own job: a hall display can't bump
// orders, and a kiosk can't read the kitchen queue.
func RequireDeviceType(types ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims := DeviceClaims(c)
		if claims == nil {
			return unauthorized(c, "missing device context")
		}
		if !slices.Contains(types, claims.DeviceType) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "wrong_device_type",
					"message": "this device type cannot use this endpoint",
				},
			})
		}
		return c.Next()
	}
}
