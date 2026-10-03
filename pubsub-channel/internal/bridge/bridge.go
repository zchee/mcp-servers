// Package bridge pulls messages from a Pub/Sub subscription and delivers each
// one to a Claude Code channel.
package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	"golang.org/x/time/rate"

	"github.com/zchee/mcp-servers/pubsub-channel/internal/event"
)

const (
	// DefaultMaxOutstandingMessages bounds how many messages are being
	// delivered at once. It limits concurrency, not throughput: a delivery
	// takes microseconds, so it does not slow down a backlog. The rate limit
	// does that.
	DefaultMaxOutstandingMessages = 10

	// DefaultMaxRate is the default number of events per second delivered to
	// the session once the burst is spent. Anyone who can publish to the topic
	// can fill the model's context, so the default is low.
	DefaultMaxRate = 1.0

	// DefaultMaxBurst is the default number of events delivered at once before
	// the rate limit applies.
	DefaultMaxBurst = 10

	// shutdownGrace is how long Receive waits for handlers in flight after its
	// context is cancelled. A handler waiting for the rate limit ends almost at
	// once then, because the wait fails on the cancelled context; a handler
	// blocked in a write to a client that stopped reading is abandoned when the
	// grace period ends.
	shutdownGrace = 2 * time.Second
)

// Notifier delivers one channel event. A nil error means the event was written
// to the session; it is the only delivery signal a channel has.
type Notifier interface {
	Notify(ctx context.Context, content string, meta map[string]string) error
}

// settler acknowledges or rejects a received message. *pubsub.Message
// implements it.
type settler interface {
	Ack()
	Nack()
}

// Bridge delivers Pub/Sub messages to a Notifier.
type Bridge struct {
	notifier Notifier
	opts     event.Options
	limiter  *rate.Limiter
	logger   *slog.Logger
}

// New returns a Bridge that maps messages with opts and delivers them to n, no
// faster than limiter allows. A limiter with limit [rate.Inf] does not limit.
func New(n Notifier, opts event.Options, limiter *rate.Limiter, logger *slog.Logger) *Bridge {
	return &Bridge{notifier: n, opts: opts, limiter: limiter, logger: logger}
}

// NewLimiter returns the limiter for maxRate events per second with bursts of
// maxBurst events. A maxRate of zero means no limit.
func NewLimiter(maxRate float64, maxBurst int) *rate.Limiter {
	if maxRate == 0 {
		return rate.NewLimiter(rate.Inf, 0)
	}
	return rate.NewLimiter(rate.Limit(maxRate), maxBurst)
}

// Run receives messages from sub until ctx is cancelled or receiving fails.
// It returns nil when ctx was cancelled and the receive error otherwise.
//
// Run replaces sub.ReceiveSettings.ShutdownOptions with a bounded shutdown.
// The library's default waits for every in-flight message to be settled before
// it cancels the pull stream, and a stream that is still retrying its first
// connection (Pub/Sub unreachable) can only be interrupted by that cancel, so
// Receive would never return. A bounded shutdown cancels the pull stream first
// and then waits at most shutdownGrace for handlers in flight.
//
// Once the pull stream is cancelled, the library sends no more acks or nacks,
// including those still waiting for their 100ms batch. At shutdown, therefore:
// a message whose handler nacks is not redelivered at once but only when its
// lease expires (in production the lease follows the 99th percentile of
// observed ack times, from 10s up to 10 minutes; on the emulator it was about
// 7s); a message acked during the grace period
// keeps its lease too, so Pub/Sub delivers it again later as a duplicate; and a
// handler still blocked in a write when the grace period ends is abandoned with
// its message neither acked nor nacked, possibly leaving a partial frame in the
// pipe.
func (b *Bridge) Run(ctx context.Context, sub *pubsub.Subscriber) error {
	sub.ReceiveSettings.ShutdownOptions = &pubsub.ShutdownOptions{
		Timeout:  shutdownGrace,
		Behavior: pubsub.ShutdownBehaviorWaitForProcessing,
	}
	// Deliveries use ctx rather than the per-message context: the per-message
	// context belongs to Pub/Sub's flow control, while the write belongs to the
	// session, which outlives any single message.
	err := sub.Receive(ctx, func(_ context.Context, m *pubsub.Message) {
		_ = b.handle(ctx, m, m)
	})
	// Cancelling the pull stream can surface as a gRPC Canceled status, which
	// does not match context.Canceled; once ctx is done, any error is the
	// shutdown itself.
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("receive from %s: %w", sub, err)
	}
	return nil
}

// handle waits for the rate limiter, delivers m and settles it through s: ack
// when the notification was written, nack otherwise so Pub/Sub redelivers it.
// A wait that ctx ends, at shutdown, nacks the message without delivering it.
//
// Claude Code never acknowledges an event, so an ack here only means the event
// was written to the session.
func (b *Bridge) handle(ctx context.Context, m *pubsub.Message, s settler) error {
	if err := b.limiter.Wait(ctx); err != nil {
		s.Nack()
		b.logger.DebugContext(ctx, "rate-limit wait ended; message nacked", "message_id", m.ID, "error", err)
		return err
	}
	ev := event.FromMessage(m, b.opts)
	if err := b.notifier.Notify(ctx, ev.Content, ev.Meta); err != nil {
		s.Nack()
		b.logger.WarnContext(ctx, "delivery failed; message nacked", "message_id", m.ID, "error", err)
		return err
	}
	s.Ack()
	b.logger.DebugContext(ctx, "message delivered and acked", "message_id", m.ID, "content_bytes", len(ev.Content))
	return nil
}
