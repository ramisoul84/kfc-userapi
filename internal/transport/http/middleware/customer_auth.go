package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
)

const ContextCustomerClaims = "customer_claims"

// CustomerAuth requires a valid customer access token (website).
func CustomerAuth(tokens *jwt.CustomerTokenManager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw, err := extractBearer(c)
		if err != nil {
			return unauthorized(c, err.Error())
		}
		claims, err := tokens.VerifyAccess(raw)
		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				return unauthorizedCode(c, "token_expired", "session expired")
			}
			return unauthorizedCode(c, "invalid_token", "invalid token")
		}
		c.Locals(ContextCustomerClaims, claims)
		return c.Next()
	}
}

// CustomerClaims returns the claims set by CustomerAuth, or nil.
func CustomerClaims(c *fiber.Ctx) *domain.CustomerClaims {
	if v, ok := c.Locals(ContextCustomerClaims).(*domain.CustomerClaims); ok {
		return v
	}
	return nil
}
