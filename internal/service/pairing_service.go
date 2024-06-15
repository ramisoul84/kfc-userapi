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
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// PairingService handles pairing code storage and validation.
type PairingService interface {
	CacheCodes(ctx context.Context, req *CacheCodesRequest) (int, error)
	CacheCode(ctx context.Context, req *CacheCodeRequest) error
	Revoke(ctx context.Context, serial string) error
}

// ─────────────────────────────────────────────────────────────────
// Inputs
// ─────────────────────────────────────────────────────────────────

type CacheCodesRequest struct {
	RestaurantID uuid.UUID
	ExpiresAt    time.Time
	Codes        []CodeEntry
}

type CodeEntry struct {
	SerialNumber string
	DeviceID     uuid.UUID
	Code         string // plaintext; hashed before storage
}

type CacheCodeRequest struct {
	RestaurantID uuid.UUID
	SerialNumber string
	DeviceID     uuid.UUID
	Code         string
	ExpiresAt    time.Time
}

// ─────────────────────────────────────────────────────────────────
// Implementation
// ─────────────────────────────────────────────────────────────────

type pairingService struct {
	repo   repository.PairingRepository
	logger *logger.Logger
}

func NewPairingService(repo repository.PairingRepository, log *logger.Logger) PairingService {
	return &pairingService{repo: repo, logger: log}
}

func (s *pairingService) CacheCodes(ctx context.Context, req *CacheCodesRequest) (int, error) {
	if len(req.Codes) == 0 {
		return 0, domain.NewValidationError("no codes provided")
	}
	if !req.ExpiresAt.After(time.Now()) {
		return 0, domain.NewValidationError("expires_at must be in the future")
	}

	ttl := time.Until(req.ExpiresAt)
	entries := make([]repository.RestaurantCodeEntry, 0, len(req.Codes))

	for _, c := range req.Codes {
		if c.SerialNumber == "" || c.Code == "" {
			return 0, domain.NewValidationError("serial_number and code are required")
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(c.Code), bcrypt.DefaultCost)
		if err != nil {
			return 0, domain.NewInternalError(err)
		}

		entries = append(entries, repository.RestaurantCodeEntry{
			Serial:   c.SerialNumber,
			CodeHash: string(hash),
			Meta: &repository.PairingCodeData{
				DeviceID:     c.DeviceID,
				RestaurantID: req.RestaurantID,
				ExpiresAt:    req.ExpiresAt,
			},
		})
	}

	if err := s.repo.ReplaceRestaurantCodes(ctx, req.RestaurantID, entries, ttl); err != nil {
		return 0, err
	}

	s.logger.Info("pairing codes cached",
		"restaurant_id", req.RestaurantID,
		"count", len(entries),
		"expires_at", req.ExpiresAt,
	)
	return len(entries), nil
}

func (s *pairingService) CacheCode(ctx context.Context, req *CacheCodeRequest) error {
	if req.SerialNumber == "" || req.Code == "" {
		return domain.NewValidationError("serial_number and code are required")
	}
	if !req.ExpiresAt.After(time.Now()) {
		return domain.NewValidationError("expires_at must be in the future")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Code), bcrypt.DefaultCost)
	if err != nil {
		return domain.NewInternalError(err)
	}

	if err := s.repo.SetCode(ctx, req.SerialNumber, string(hash), &repository.PairingCodeData{
		DeviceID:     req.DeviceID,
		RestaurantID: req.RestaurantID,
		ExpiresAt:    req.ExpiresAt,
	}); err != nil {
		return err
	}

	s.logger.Info("pairing code cached",
		"serial_number", req.SerialNumber,
		"device_id", req.DeviceID,
		"expires_at", req.ExpiresAt,
	)
	return nil
}

func (s *pairingService) Revoke(ctx context.Context, serial string) error {
	if serial == "" {
		return domain.NewValidationError("serial_number is required")
	}
	if err := s.repo.DeleteCode(ctx, serial); err != nil {
		return err
	}
	s.logger.Info("pairing code revoked", "serial_number", serial)
	return nil
}

// Compile-time guard: keep errors import if unused elsewhere.
var _ = errors.New
var _ = fmt.Sprintf
