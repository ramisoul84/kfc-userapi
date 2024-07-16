package service

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	menuv1 "github.com/ramisoul84/kfc-userapi/gen/menu/v1"
	"github.com/ramisoul84/kfc-userapi/internal/client"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

var ErrMenuNotFound = errors.New("menu not found")

type MenuService interface {
	GetEffectiveMenu(ctx context.Context, restaurantID string) ([]byte, error)

	// GetMenu returns the same cached menu decoded, for server-side
	// pricing of orders.
	GetMenu(ctx context.Context, restaurantID string) (*menuv1.GetEffectiveMenuResponse, error)
}

type menuService struct {
	cache  repository.MenuCacheRepository
	crm    client.CRMClient
	logger *logger.Logger
}

func NewMenuService(
	cache repository.MenuCacheRepository,
	crm client.CRMClient,
	log *logger.Logger,
) MenuService {
	return &menuService{cache: cache, crm: crm, logger: log}
}

// GetEffectiveMenu returns the effective menu as JSON bytes.
//
// Cache-aside: Redis first, then CRM over gRPC on miss. The protobuf
// response from CRM is converted to JSON once and cached; subsequent
// requests serve the cached bytes without re-marshaling.
func (s *menuService) GetEffectiveMenu(
	ctx context.Context,
	restaurantID string,
) ([]byte, error) {
	// 1. Cache lookup.
	cached, err := s.cache.Get(ctx, restaurantID)
	if err != nil {
		s.logger.Warn("menu cache read failed",
			"restaurant_id", restaurantID,
			"error", err,
		)
		// Fall through to CRM.
	} else if cached != nil {
		s.logger.Debug("menu cache hit",
			"restaurant_id", restaurantID,
			"bytes", len(cached),
		)
		return cached, nil
	}

	s.logger.Debug("menu cache miss", "restaurant_id", restaurantID)

	// 2. Fetch from CRM over gRPC.
	resp, err := s.crm.GetEffectiveMenu(ctx, restaurantID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrMenuNotFound
		}
		return nil, fmt.Errorf("fetch effective menu: %w", err)
	}

	// 3. Convert protobuf → JSON once.
	payload, err := protojson.MarshalOptions{
		UseProtoNames: true,
	}.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal menu to json: %w", err)
	}

	// 4. Cache the JSON so future hits skip the conversion.
	if err := s.cache.Set(ctx, restaurantID, payload); err != nil {
		s.logger.Warn("menu cache write failed",
			"restaurant_id", restaurantID,
			"error", err,
		)
		// Not fatal — we still return the data.
	}

	return payload, nil
}

func (s *menuService) GetMenu(
	ctx context.Context,
	restaurantID string,
) (*menuv1.GetEffectiveMenuResponse, error) {
	payload, err := s.GetEffectiveMenu(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	var menu menuv1.GetEffectiveMenuResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, &menu); err != nil {
		return nil, fmt.Errorf("decode cached menu: %w", err)
	}
	return &menu, nil
}
