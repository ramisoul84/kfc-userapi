package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	menuv1 "github.com/ramisoul84/kfc-userapi/gen/menu/v1"
	"github.com/ramisoul84/kfc-userapi/internal/config"
)

// CRMClient is the subset of CRM's gRPC surface that userapi needs.
// Kept as an interface so tests can substitute a fake.
type CRMClient interface {
	GetEffectiveMenu(ctx context.Context, restaurantID string) (*menuv1.GetEffectiveMenuResponse, error)
	Close() error
}

type crmClient struct {
	client  menuv1.MenuServiceClient
	conn    *grpc.ClientConn
	timeout time.Duration
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
		client:  menuv1.NewMenuServiceClient(conn),
		conn:    conn,
		timeout: cfg.Timeout,
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

func (c *crmClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
