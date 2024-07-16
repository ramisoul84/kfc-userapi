package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	orderv1 "github.com/ramisoul84/kfc-userapi/gen/order/v1"
	"github.com/ramisoul84/kfc-userapi/internal/config"
)

// OrderClient is the subset of kfc-order that userapi uses.
type OrderClient interface {
	CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.Order, error)
	GetOrder(ctx context.Context, id string) (*orderv1.Order, error)
	ListActiveOrders(ctx context.Context, restaurantID string, statuses []string) ([]*orderv1.Order, error)
	UpdateOrderStatus(ctx context.Context, req *orderv1.UpdateOrderStatusRequest) (*orderv1.Order, error)
	Close() error
}

type orderClient struct {
	client  orderv1.OrderServiceClient
	conn    *grpc.ClientConn
	timeout time.Duration
}

func NewOrderClient(cfg *config.OrderConfig) (OrderClient, error) {
	conn, err := grpc.NewClient(
		cfg.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create order grpc client: %w", err)
	}
	return &orderClient{
		client:  orderv1.NewOrderServiceClient(conn),
		conn:    conn,
		timeout: cfg.Timeout,
	}, nil
}

func (c *orderClient) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.CreateOrder(ctx, req)
}

func (c *orderClient) GetOrder(ctx context.Context, id string) (*orderv1.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.GetOrder(ctx, &orderv1.GetOrderRequest{Id: id})
}

func (c *orderClient) ListActiveOrders(ctx context.Context, restaurantID string, statuses []string) ([]*orderv1.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	resp, err := c.client.ListActiveOrders(ctx, &orderv1.ListActiveOrdersRequest{
		RestaurantId: restaurantID,
		Statuses:     statuses,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetOrders(), nil
}

func (c *orderClient) UpdateOrderStatus(ctx context.Context, req *orderv1.UpdateOrderStatusRequest) (*orderv1.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.UpdateOrderStatus(ctx, req)
}

func (c *orderClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
