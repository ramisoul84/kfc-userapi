package grpc

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	devicepairingv1 "github.com/ramisoul84/kfc-userapi/gen/devicepairing/v1"
	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// Transport wraps the gRPC server with lifecycle methods.
type Transport struct {
	srv    *grpc.Server
	cfg    *config.Config
	logger *logger.Logger
}

func NewTransport(
	cfg *config.Config,
	log *logger.Logger,
	devicePairingServerServer *DevicePairingServer,
) *Transport {
	srv := grpc.NewServer()

	devicepairingv1.RegisterDevicePairingServiceServer(srv, devicePairingServerServer)

	return &Transport{srv: srv, cfg: cfg, logger: log}
}

func (t *Transport) Start() error {
	addr := ":" + t.cfg.GRPC.Port
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	t.logger.Info("grpc userapi server listening", "addr", addr)
	if err := t.srv.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

func (t *Transport) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		t.srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.srv.Stop()
		return ctx.Err()
	}
}
