package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notificationv1 "github.com/ramisoul84/kfc-userapi/gen/notification/v1"
	"github.com/ramisoul84/kfc-userapi/internal/config"
)

// NotificationClient sends customer sign-in codes by SMS.
type NotificationClient struct {
	client notificationv1.NotificationServiceClient
	conn   *grpc.ClientConn
}

func NewNotificationClient(cfg *config.NotificationConfig) (*NotificationClient, error) {
	conn, err := grpc.NewClient(
		cfg.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(timeoutInterceptor(cfg.Timeout)),
	)
	if err != nil {
		return nil, fmt.Errorf("create notification grpc client: %w", err)
	}
	return &NotificationClient{client: notificationv1.NewNotificationServiceClient(conn), conn: conn}, nil
}

func (c *NotificationClient) SendLoginCode(ctx context.Context, phone, code string, validFor time.Duration) error {
	_, err := c.client.SendLoginCode(ctx, &notificationv1.SendLoginCodeRequest{
		Phone:        phone,
		Code:         code,
		ValidSeconds: int32(validFor.Seconds()),
	})
	if err != nil {
		return fmt.Errorf("send login code: %w", err)
	}
	return nil
}

func (c *NotificationClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
