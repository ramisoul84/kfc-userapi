package response

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
)

// Error is the JSON shape for a failed request.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorBody wraps an Error under the "error" key.
type ErrorBody struct {
	Error Error `json:"error"`
}

// OK writes a 200 response with the given payload.
func OK(c *fiber.Ctx, payload any) error {
	return c.Status(fiber.StatusOK).JSON(payload)
}

// Created writes a 201 response.
func Created(c *fiber.Ctx, payload any) error {
	return c.Status(fiber.StatusCreated).JSON(payload)
}

// BadRequest writes a 400 with an error body.
func BadRequest(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusBadRequest, "bad_request", message)
}

// Unauthorized writes a 401 with an error code.
func Unauthorized(c *fiber.Ctx, code, message string) error {
	return ErrorResponse(c, fiber.StatusUnauthorized, code, message)
}

// Forbidden writes a 403 with an error body.
func Forbidden(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusForbidden, "forbidden", message)
}

// NotFound writes a 404 with an error body.
func NotFound(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusNotFound, "not_found", message)
}

// Conflict writes a 409 with an error body.
func Conflict(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusConflict, "conflict", message)
}

// Internal writes a 500 with a generic message (never leak internals).
func Internal(c *fiber.Ctx) error {
	return ErrorResponse(c, fiber.StatusInternalServerError, "internal", "internal server error")
}

// ErrorResponse is the primitive used by all the helpers above.
func ErrorResponse(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(ErrorBody{
		Error: Error{
			Code:    code,
			Message: message,
		},
	})
}

// FromError maps a domain error to the right HTTP response.
func FromError(c *fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}

	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		switch appErr.Type {
		case domain.ErrorTypeValidation:
			return ErrorResponse(c, fiber.StatusBadRequest, "validation", appErr.Message)
		case domain.ErrorTypeAuthentication:
			return ErrorResponse(c, fiber.StatusUnauthorized, "unauthorized", appErr.Message)
		case domain.ErrorTypeAuthorization:
			return ErrorResponse(c, fiber.StatusForbidden, "forbidden", appErr.Message)
		case domain.ErrorTypeNotFound:
			return ErrorResponse(c, fiber.StatusNotFound, "not_found", appErr.Message)
		case domain.ErrorTypeConflict:
			return ErrorResponse(c, fiber.StatusConflict, "conflict", appErr.Message)
		case domain.ErrorTypeInternal:
			// Do not leak appErr.Err to the client.
			return Internal(c)
		}
	}

	return Internal(c)
}
