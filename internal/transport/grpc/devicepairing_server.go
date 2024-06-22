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
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// UserAPIServer implements userapiv1.UserAPIServiceServer.
type DevicePairingServer struct {
	devicepairingv1.UnimplementedDevicePairingServiceServer

	svc    service.PairingService
	logger *logger.Logger
}

func NewUserAPIServer(svc service.PairingService, log *logger.Logger) *DevicePairingServer {
	return &DevicePairingServer{svc: svc, logger: log}
}

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
			return nil, status.Errorf(codes.InvalidArgument, "invalid device_id for serial %s", c.GetSerialNumber())
		}
		entries = append(entries, service.CodeEntry{
			SerialNumber: c.GetSerialNumber(),
			DeviceID:     deviceID,
			Code:         c.GetCode(),
		})
	}

	cached, err := s.svc.CacheCodes(ctx, &service.CacheCodesRequest{
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

	err = s.svc.CacheCode(ctx, &service.CacheCodeRequest{
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

func (s *DevicePairingServer) RevokePairingCode(
	ctx context.Context,
	req *devicepairingv1.RevokePairingCodeRequest,
) (*devicepairingv1.RevokePairingCodeResponse, error) {
	if err := s.svc.Revoke(ctx, req.GetSerialNumber()); err != nil {
		return nil, mapError(err, s.logger)
	}
	return &devicepairingv1.RevokePairingCodeResponse{
		Success: true,
		Message: "code revoked",
	}, nil
}

// mapError converts domain errors to gRPC codes.
func mapError(err error, log *logger.Logger) error {
	switch {
	case domain.IsValidationError(err):
		return status.Error(codes.InvalidArgument, err.Error())
	case domain.IsNotFoundError(err):
		return status.Error(codes.NotFound, err.Error())
	case domain.IsInternalError(err):
		log.Error("grpc internal error", "error", err)
		return status.Error(codes.Internal, "internal error")
	default:
		log.Error("grpc unhandled error", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
