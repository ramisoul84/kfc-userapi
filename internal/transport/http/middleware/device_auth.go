package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
)

const ContextDeviceClaims = "device_claims"

func DeviceAuth(tokens *jwt.DeviceTokenManager, revoke repository.RevocationRepository) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw, err := extractBearer(c)
		if err != nil {
			return unauthorized(c, err.Error())
		}

		claims, err := tokens.Verify(raw)
		if err != nil {
			switch {
			case errors.Is(err, jwt.ErrTokenExpired):
				return unauthorizedCode(c, "token_expired", "device token expired; re-pair required")
			default:
				return unauthorizedCode(c, "invalid_token", "invalid token")
			}
		}

		// Revocation check — cheap Redis EXISTS.
		revoked, err := revoke.IsDeviceRevoked(c.Context(), claims.DeviceID)
		if err != nil {
			// Fail closed: if we can't verify revocation, deny.
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": fiber.Map{"code": "auth_unavailable", "message": "authorization unavailable"},
			})
		}
		if revoked {
			return unauthorizedCode(c, "revoked", "device access revoked")
		}

		revokedToken, err := revoke.IsTokenRevoked(c.Context(), claims.TokenID)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": fiber.Map{"code": "auth_unavailable", "message": "authorization unavailable"},
			})
		}
		if revokedToken {
			return unauthorizedCode(c, "revoked", "token revoked")
		}

		c.Locals(ContextDeviceClaims, claims)
		return c.Next()
	}
}

// DeviceClaimsFromCtx returns the claims set by DeviceAuth.
func DeviceClaimsFromCtx(c *fiber.Ctx) *domain.DeviceTokenClaims {
	if v, ok := c.Locals(ContextDeviceClaims).(*domain.DeviceTokenClaims); ok {
		return v
	}
	return nil
}

func extractBearer(c *fiber.Ctx) (string, error) {
	auth := c.Get("Authorization")
	if auth == "" {
		return "", errors.New("missing authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", errors.New("invalid authorization scheme")
	}
	raw := strings.TrimPrefix(auth, prefix)
	if raw == "" {
		return "", errors.New("empty token")
	}
	return raw, nil
}

func unauthorized(c *fiber.Ctx, msg string) error {
	return unauthorizedCode(c, "unauthorized", msg)
}

func unauthorizedCode(c *fiber.Ctx, code, msg string) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": fiber.Map{"code": code, "message": msg},
	})
}

// DeviceClaims returns the claims set by DeviceAuth, or nil.
func DeviceClaims(c *fiber.Ctx) *domain.DeviceTokenClaims {
	if v, ok := c.Locals(ContextDeviceClaims).(*domain.DeviceTokenClaims); ok {
		return v
	}
	return nil
}

// Compile guard.
var _ = context.Background
