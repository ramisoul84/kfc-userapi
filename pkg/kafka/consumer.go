package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// MessageHandler processes a single Kafka message.
//
// Returning an error causes the consumer to log it. The offset is
// committed regardless, because our handlers are idempotent (deleting
// a cache key twice is harmless). Stuck messages are worse than the
// occasional redundant invalidation.
type MessageHandler func(ctx context.Context, key string, value []byte) error

// Consumer wraps a kafka-go Reader with a consumer group and a handler.
type Consumer struct {
	reader  *kafka.Reader
	handler MessageHandler
	logger  *logger.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

// Option tweaks the reader configuration.
type Option func(*kafka.ReaderConfig)

// StartAtLatest makes a brand-new consumer group start from the newest
// message instead of the oldest. Use it for live fan-out (SSE), where
// replaying history to screens would be wrong.
func StartAtLatest() Option {
	return func(c *kafka.ReaderConfig) { c.StartOffset = kafka.LastOffset }
}

func NewConsumer(
	brokers []string,
	topic string,
	groupID string,
	handler MessageHandler,
	log *logger.Logger,
	opts ...Option,
) *Consumer {
	cfg := kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,    // don't wait for a batch
		MaxBytes:       10e6, // 10 MB
		MaxWait:        500 * time.Millisecond,
		StartOffset:    kafka.FirstOffset, // new groups read from beginning
		CommitInterval: 0,                 // commit synchronously
		ReadBackoffMin: 100 * time.Millisecond,
		ReadBackoffMax: 1 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	r := kafka.NewReader(cfg)

	return &Consumer{
		reader:  r,
		handler: handler,
		logger:  log,
		done:    make(chan struct{}),
	}
}

// Start begins consuming in a background goroutine and returns immediately.
func (c *Consumer) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		defer close(c.done)

		c.logger.Info("kafka consumer started",
			"topic", c.reader.Config().Topic,
			"group", c.reader.Config().GroupID,
			"brokers", c.reader.Config().Brokers,
		)

		for {
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					c.logger.Info("kafka consumer stopping")
					return
				}
				c.logger.Error("kafka fetch failed", "error", err)
				time.Sleep(time.Second)
				continue
			}

			c.logger.Debug("kafka message received",
				"topic", msg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
				"key", string(msg.Key),
			)

			if err := c.handler(ctx, string(msg.Key), msg.Value); err != nil {
				c.logger.Error("kafka handler failed",
					"topic", msg.Topic,
					"partition", msg.Partition,
					"offset", msg.Offset,
					"error", err,
				)
				// Fall through and commit anyway — see type doc.
			}

			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				c.logger.Error("kafka commit failed",
					"partition", msg.Partition,
					"offset", msg.Offset,
					"error", err,
				)
			}
		}
	}()
}

// Stop cancels the consuming loop and closes the reader.
func (c *Consumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	<-c.done

	if c.reader != nil {
		if err := c.reader.Close(); err != nil {
			return fmt.Errorf("close kafka reader: %w", err)
		}
	}
	return nil
}
