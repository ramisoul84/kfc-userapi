package grpc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	devicepairingv1 "github.com/ramisoul84/kfc-userapi/gen/devicepairing/v1"
	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// DevicePairingServer implements devicepairingv1.DevicePairingServiceServer.
type DevicePairingServer struct {
	devicepairingv1.UnimplementedDevicePairingServiceServer

	pairingSvc service.PairingService
	authSvc    service.DeviceAuthService
	tokens     *jwt.DeviceTokenManager
	logger     *logger.Logger
}

func NewDevicePairingServer(
	pairingSvc service.PairingService,
	authSvc service.DeviceAuthService,
	tokens *jwt.DeviceTokenManager,
	log *logger.Logger,
) *DevicePairingServer {
	return &DevicePairingServer{
		pairingSvc: pairingSvc,
		authSvc:    authSvc,
		tokens:     tokens,
		logger:     log,
	}
}

// ═══════════════════════════════════════════════════════════════════
// CACHE PAIRING CODES (batch)
// ═══════════════════════════════════════════════════════════════════

func (s *DevicePairingServer) CachePairingCodes(
	ctx context.Context,
	req *devicepairingv1.CachePairingCodesRequest,
) (*devicepairingv1.CachePairingCodesResponse, error) {
	restaurantID, err := uuid.Parse(req.GetRestaurantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid restaurant_id")
	}

	entries := make([]service.CodeEntry, 0, len(req.GetCodes()))
	for _, c := range req.GetCodes() {
		deviceID, err := uuid.Parse(c.GetDeviceId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"invalid device_id for serial %s", c.GetSerialNumber())
		}
		entries = append(entries, service.CodeEntry{
			SerialNumber: c.GetSerialNumber(),
			DeviceID:     deviceID,
			Code:         c.GetCode(),
		})
	}

	cached, err := s.pairingSvc.CacheCodes(ctx, &service.CacheCodesRequest{
		RestaurantID: restaurantID,
		ExpiresAt:    time.Unix(req.GetExpiresAt(), 0).UTC(),
		Codes:        entries,
	})
	if err != nil {
		return nil, mapError(err, s.logger)
	}

	return &devicepairingv1.CachePairingCodesResponse{
		Success: true,
		Cached:  int32(cached),
		Message: "codes cached",
	}, nil
}

// ═══════════════════════════════════════════════════════════════════
// CACHE PAIRING CODE (single)
// ═══════════════════════════════════════════════════════════════════

func (s *DevicePairingServer) CachePairingCode(
	ctx context.Context,
	req *devicepairingv1.CachePairingCodeRequest,
) (*devicepairingv1.CachePairingCodeResponse, error) {
	restaurantID, err := uuid.Parse(req.GetRestaurantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid restaurant_id")
	}
	deviceID, err := uuid.Parse(req.GetDeviceId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid device_id")
	}

	err = s.pairingSvc.CacheCode(ctx, &service.CacheCodeRequest{
		RestaurantID: restaurantID,
		SerialNumber: req.GetSerialNumber(),
		DeviceID:     deviceID,
		Code:         req.GetCode(),
		ExpiresAt:    time.Unix(req.GetExpiresAt(), 0).UTC(),
	})
	if err != nil {
		return nil, mapError(err, s.logger)
	}

	return &devicepairingv1.CachePairingCodeResponse{
		Success: true,
		Message: "code cached",
	}, nil
}

// ═══════════════════════════════════════════════════════════════════
// REVOKE PAIRING CODE (by serial)
// ═══════════════════════════════════════════════════════════════════

func (s *DevicePairingServer) RevokePairingCode(
	ctx context.Context,
	req *devicepairingv1.RevokePairingCodeRequest,
) (*devicepairingv1.RevokePairingCodeResponse, error) {
	if err := s.pairingSvc.Revoke(ctx, req.GetSerialNumber()); err != nil {
		return nil, mapError(err, s.logger)
	}
	return &devicepairingv1.RevokePairingCodeResponse{
		Success: true,
		Message: "pairing code revoked",
	}, nil
}

// ═══════════════════════════════════════════════════════════════════
// REVOKE DEVICE (invalidate all its tokens)
// ═══════════════════════════════════════════════════════════════════

func (s *DevicePairingServer) RevokeDevice(
	ctx context.Context,
	req *devicepairingv1.RevokeDeviceRequest,
) (*devicepairingv1.RevokeDeviceResponse, error) {
	deviceID, err := uuid.Parse(req.GetDeviceId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid device_id")
	}

	if err := s.authSvc.Revoke(ctx, deviceID); err != nil {
		return nil, mapError(err, s.logger)
	}

	// Revocation entries live as long as a token would. If the caller
	// passed a ttl_seconds, it's informational only — the manager TTL
	// comes from JWT_DEVICE_TTL and matches how long tokens can live.
	// (If you want the caller to override, change DeviceAuthService.Revoke
	// to accept a ttl, but for now the manager TTL is the source of truth.)
	_ = req.GetTtlSeconds()

	return &devicepairingv1.RevokeDeviceResponse{
		Success: true,
		Message: "device revoked",
	}, nil
}

// ═══════════════════════════════════════════════════════════════════
// ERROR MAPPING
// ═══════════════════════════════════════════════════════════════════

// mapError converts domain.AppError types to gRPC codes.
func mapError(err error, log *logger.Logger) error {
	switch {
	case domain.IsValidationError(err):
		return status.Error(codes.InvalidArgument, err.Error())
	case domain.IsAuthenticationError(err):
		return status.Error(codes.Unauthenticated, err.Error())
	case domain.IsAuthorizationError(err):
		return status.Error(codes.PermissionDenied, err.Error())
	case domain.IsNotFoundError(err):
		return status.Error(codes.NotFound, err.Error())
	case domain.IsConflictError(err):
		return status.Error(codes.AlreadyExists, err.Error())
	case domain.IsInternalError(err):
		log.Error("grpc internal error", "error", err)
		return status.Error(codes.Internal, "internal error")
	default:
		log.Error("grpc unhandled error", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
