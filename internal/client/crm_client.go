package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	menuv1 "github.com/ramisoul84/kfc-userapi/gen/menu/v1"
	restaurantv1 "github.com/ramisoul84/kfc-userapi/gen/restaurant/v1"
	"github.com/ramisoul84/kfc-userapi/internal/config"
)

// CRMClient is the subset of CRM's gRPC surface that userapi needs.
// Kept as an interface so tests can substitute a fake.
type CRMClient interface {
	GetEffectiveMenu(ctx context.Context, restaurantID string) (*menuv1.GetEffectiveMenuResponse, error)
	ListRegions(ctx context.Context) ([]*restaurantv1.Region, error)
	ListRestaurants(ctx context.Context, regionID string) ([]*restaurantv1.Restaurant, error)
	GetRestaurant(ctx context.Context, id string) (*restaurantv1.Restaurant, error)
	Close() error
}

type crmClient struct {
	client    menuv1.MenuServiceClient
	directory restaurantv1.RestaurantDirectoryClient
	conn      *grpc.ClientConn
	timeout   time.Duration
}

func NewCRMClient(cfg *config.CRMConfig) (CRMClient, error) {
	conn, err := grpc.NewClient(
		cfg.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create crm grpc client: %w", err)
	}

	return &crmClient{
		client:    menuv1.NewMenuServiceClient(conn),
		directory: restaurantv1.NewRestaurantDirectoryClient(conn),
		conn:      conn,
		timeout:   cfg.Timeout,
	}, nil
}

func (c *crmClient) GetEffectiveMenu(
	ctx context.Context,
	restaurantID string,
) (*menuv1.GetEffectiveMenuResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.GetEffectiveMenu(ctx, &menuv1.GetEffectiveMenuRequest{
		RestaurantId: restaurantID,
	})
	if err != nil {
		return nil, fmt.Errorf("crm GetEffectiveMenu: %w", err)
	}
	return resp, nil
}

func (c *crmClient) ListRegions(ctx context.Context) ([]*restaurantv1.Region, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.directory.ListRegions(ctx, &restaurantv1.ListRegionsRequest{})
	if err != nil {
		return nil, fmt.Errorf("crm ListRegions: %w", err)
	}
	return resp.GetRegions(), nil
}

func (c *crmClient) ListRestaurants(ctx context.Context, regionID string) ([]*restaurantv1.Restaurant, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.directory.ListRestaurants(ctx, &restaurantv1.ListRestaurantsRequest{RegionId: regionID})
	if err != nil {
		return nil, fmt.Errorf("crm ListRestaurants: %w", err)
	}
	return resp.GetRestaurants(), nil
}

func (c *crmClient) GetRestaurant(ctx context.Context, id string) (*restaurantv1.Restaurant, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.directory.GetRestaurant(ctx, &restaurantv1.GetRestaurantRequest{Id: id})
	if err != nil {
		return nil, fmt.Errorf("crm GetRestaurant: %w", err)
	}
	return resp, nil
}

func (c *crmClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
