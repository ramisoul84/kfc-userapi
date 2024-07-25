package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	loyaltyv1 "github.com/ramisoul84/kfc-userapi/gen/loyalty/v1"
	"github.com/ramisoul84/kfc-userapi/internal/config"
)

// LoyaltyClient wraps kfc-loyalty's gRPC API with per-call timeouts.
type LoyaltyClient struct {
	loyaltyv1.LoyaltyServiceClient
	conn    *grpc.ClientConn
	timeout time.Duration
}

func NewLoyaltyClient(cfg *config.LoyaltyConfig) (*LoyaltyClient, error) {
	conn, err := grpc.NewClient(
		cfg.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(timeoutInterceptor(cfg.Timeout)),
	)
	if err != nil {
		return nil, fmt.Errorf("create loyalty grpc client: %w", err)
	}
	return &LoyaltyClient{
		LoyaltyServiceClient: loyaltyv1.NewLoyaltyServiceClient(conn),
		conn:                 conn,
		timeout:              cfg.Timeout,
	}, nil
}

func (c *LoyaltyClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// timeoutInterceptor bounds every unary call that has no earlier deadline.
func timeoutInterceptor(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok && d > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
