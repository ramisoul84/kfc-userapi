package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// DeviceAuthService validates pairing codes and issues device tokens.
//
// Flow:
//  1. Device submits serial + code (over HTTP, no token yet).
//  2. Service looks up the code hash in Redis (via PairingRepository).
//  3. bcrypt-compares the submitted code against the stored hash.
//  4. Consumes the code (single-use) before issuing the token.
//  5. Issues a JWT via the DeviceTokenManager.
//
// The service is stateless apart from the repositories it depends on.
type DeviceAuthService interface {
	// Pair validates a serial+code and returns a freshly issued device
	// token. Returns an error of type validation if the code is wrong,
	// missing, or expired.
	Pair(ctx context.Context, serial, code string) (*PairResult, error)

	// Revoke invalidates all tokens for a device. Called by CRM when a
	// device is deleted, deactivated, or reported stolen.
	Revoke(ctx context.Context, deviceID uuid.UUID) error

	// RevokeToken invalidates a single token by its jti. Used when a
	// token is reported compromised but the device itself is fine.
	RevokeToken(ctx context.Context, tokenID string) error
}

// PairResult is returned on successful pairing.
type PairResult struct {
	Token     string
	ExpiresAt time.Time
	DeviceID  uuid.UUID
}

type deviceAuthService struct {
	pairing repository.PairingRepository
	revoke  repository.RevocationRepository
	tokens  *jwt.DeviceTokenManager
	logger  *logger.Logger
}

// NewDeviceAuthService constructs the service.
func NewDeviceAuthService(
	pairing repository.PairingRepository,
	revoke repository.RevocationRepository,
	tokens *jwt.DeviceTokenManager,
	log *logger.Logger,
) DeviceAuthService {
	return &deviceAuthService{
		pairing: pairing,
		revoke:  revoke,
		tokens:  tokens,
		logger:  log,
	}
}

// ═══════════════════════════════════════════════════════════════════
// PAIR
// ═══════════════════════════════════════════════════════════════════

func (s *deviceAuthService) Pair(
	ctx context.Context,
	serial, code string,
) (*PairResult, error) {
	// 1. Input validation
	if serial == "" || code == "" {
		return nil, domain.NewValidationError("serial and code are required")
	}

	// 2. Look up the stored hash and metadata.
	//
	// GetCode returns ("", nil, nil) when no code exists for the serial —
	// not an error. That lets us return the same generic message whether
	// the serial is unknown or the code is wrong, so we don't leak which
	// serials are registered.
	hash, meta, err := s.pairing.GetCode(ctx, serial)
	if err != nil {
		s.logger.Error("pair: lookup failed", "serial_number", serial, "error", err)
		return nil, err
	}

	if hash == "" || meta == nil {
		s.logger.Warn("pair: no active code",
			"serial_number", serial,
		)
		return nil, domain.NewValidationError("invalid or expired pairing code")
	}

	// 3. Verify the submitted code against the bcrypt hash.
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)); err != nil {
		s.logger.Warn("pair: code mismatch",
			"serial_number", serial,
			"device_id", meta.DeviceID,
		)
		return nil, domain.NewValidationError("invalid or expired pairing code")
	}

	// 4. Delete the code BEFORE issuing the token.
	//
	// If the delete succeeded but token issuance fails, the manager
	// re-pairs — a minor inconvenience. The opposite order would leave
	// a valid code in Redis after a successful pairing, which is worse:
	// it would allow a second device to pair with the same code.
	if err := s.pairing.DeleteCode(ctx, serial); err != nil {
		s.logger.Error("pair: consume code failed",
			"serial_number", serial,
			"device_id", meta.DeviceID,
			"error", err,
		)
		return nil, err
	}

	// 5. Issue a JWT with the device identity.
	claims, token, err := s.tokens.Issue(meta.DeviceID, serial, meta.RestaurantID)
	if err != nil {
		s.logger.Error("pair: issue token failed",
			"serial_number", serial,
			"device_id", meta.DeviceID,
			"error", err,
		)
		return nil, domain.NewInternalError(fmt.Errorf("issue token: %w", err))
	}

	s.logger.Info("device paired",
		"device_id", meta.DeviceID,
		"serial_number", serial,
		"restaurant_id", meta.RestaurantID,
		"token_id", claims.TokenID,
		"expires_at", claims.ExpiresAt,
	)

	return &PairResult{
		Token:     token,
		ExpiresAt: claims.ExpiresAt,
		DeviceID:  meta.DeviceID,
	}, nil
}

// ═══════════════════════════════════════════════════════════════════
// REVOKE
// ═══════════════════════════════════════════════════════════════════

func (s *deviceAuthService) Revoke(ctx context.Context, deviceID uuid.UUID) error {
	if deviceID == uuid.Nil {
		return domain.NewValidationError("device_id is required")
	}

	// Revocation entry lives for the same duration as a token — after
	// that, every token for the device has expired anyway.
	ttl := s.tokens.TTL()

	if err := s.revoke.RevokeDevice(ctx, deviceID, ttl); err != nil {
		s.logger.Error("revoke: failed",
			"device_id", deviceID,
			"error", err,
		)
		return err
	}

	s.logger.Info("device revoked",
		"device_id", deviceID,
		"ttl", ttl,
	)
	return nil
}

func (s *deviceAuthService) RevokeToken(ctx context.Context, tokenID string) error {
	if tokenID == "" {
		return domain.NewValidationError("token_id is required")
	}

	ttl := s.tokens.TTL()

	if err := s.revoke.RevokeToken(ctx, tokenID, ttl); err != nil {
		s.logger.Error("revoke token: failed",
			"token_id", tokenID,
			"error", err,
		)
		return err
	}

	s.logger.Info("token revoked",
		"token_id", tokenID,
		"ttl", ttl,
	)
	return nil
}

// Compile-time guard so the errors import is used even if all explicit
// error handling is refactored later.
var _ = errors.New
