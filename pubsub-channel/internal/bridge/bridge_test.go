package bridge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	gocmp "github.com/google/go-cmp/cmp"
	"golang.org/x/time/rate"

	"github.com/zchee/mcp-servers/pubsub-channel/internal/channel"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/event"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/testsupport"
)

// Compile-time check: the bridge delivers to the real channel.
var _ Notifier = (*channel.Channel)(nil)

// session is a real Channel served over pipes with a scripted client.
type session struct {
	ch      *channel.Channel
	client  *testsupport.Client
	runDone chan error
}

func startSession(t *testing.T) *session {
	t.Helper()
	client, transport := testsupport.NewClient(t)
	s := &session{
		ch:      channel.New(channel.Options{Name: "pubsub-channel-test", Version: "v0.0.0", Logger: slog.New(slog.DiscardHandler)}),
		client:  client,
		runDone: make(chan error, 1),
	}
	go func() { s.runDone <- s.ch.Run(t.Context(), transport) }()
	t.Cleanup(func() {
		select {
		case <-s.runDone:
		case <-time.After(testsupport.LineTimeout):
			t.Errorf("Channel.Run did not return within %v of the test ending", testsupport.LineTimeout)
		}
	})
	return s
}

// handshake plays initialize and notifications/initialized and waits until the
// server has processed the latter.
func (s *session) handshake(t *testing.T) {
	t.Helper()
	s.client.Initialize(t, "2025-11-25")
	s.client.SendInitialized(t)
	select {
	case <-s.ch.Initialized():
	case <-time.After(testsupport.LineTimeout):
		t.Fatalf("Initialized() was not closed within %v", testsupport.LineTimeout)
	}
}

// next returns the next line as a decoded channel notification.
func (s *session) next(t *testing.T, timeout time.Duration) testsupport.Notification {
	t.Helper()
	return testsupport.DecodeNotification(t, s.client.Next(t, timeout), channel.NotificationMethod)
}

// recorder counts how a message was settled.
type recorder struct {
	acks, nacks int
}

func (r *recorder) Ack() { r.acks++ }

func (r *recorder) Nack() { r.nacks++ }

func TestHandle(t *testing.T) {
	publishTime := time.Date(2026, 10, 3, 6, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		// prepare puts the session into the state the delivery meets.
		prepare   func(t *testing.T, s *session)
		wantAcks  int
		wantNacks int
		wantErr   error
		// wantLine reports whether a notification must reach the client.
		wantLine bool
	}{
		"success: notification written means ack": {
			prepare:  func(t *testing.T, s *session) { s.handshake(t) },
			wantAcks: 1,
			wantLine: true,
		},
		"error: write to a closed output means nack": {
			prepare: func(t *testing.T, s *session) {
				s.handshake(t)
				// The client stops reading; the next write on the pipe fails.
				s.client.CloseOutput()
			},
			wantNacks: 1,
			wantErr:   io.ErrClosedPipe,
		},
		"error: session not initialized means nack": {
			prepare:   func(*testing.T, *session) {},
			wantNacks: 1,
			wantErr:   channel.ErrNotInitialized,
		},
		"error: session ended means nack": {
			prepare: func(t *testing.T, s *session) {
				s.handshake(t)
				s.client.CloseInput()
				select {
				case err := <-s.runDone:
					s.runDone <- err
				case <-time.After(testsupport.LineTimeout):
					t.Fatalf("Channel.Run did not return within %v of client EOF", testsupport.LineTimeout)
				}
			},
			wantNacks: 1,
			wantErr:   channel.ErrClosed,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := startSession(t)
			tt.prepare(t, s)

			b := New(s.ch, event.Options{Subscription: "projects/p/subscriptions/s", MaxContentBytes: event.DefaultMaxContentBytes}, NewLimiter(0, 1), slog.New(slog.DiscardHandler))
			m := &pubsub.Message{ID: "m-1", Data: []byte("payload"), Attributes: map[string]string{"source": "ci"}, PublishTime: publishTime}
			var r recorder

			err := b.handle(t.Context(), m, &r)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("handle error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
			if diff := gocmp.Diff(recorder{acks: tt.wantAcks, nacks: tt.wantNacks}, r, gocmp.AllowUnexported(recorder{})); diff != "" {
				t.Errorf("settlement (-want +got):\n%s", diff)
			}
			if !tt.wantLine {
				return
			}
			n := s.next(t, testsupport.LineTimeout)
			want := map[string]string{
				event.KeyMessageID:    "m-1",
				event.KeyPublishTime:  "2026-10-03T06:00:00Z",
				event.KeySubscription: "projects/p/subscriptions/s",
				"attr_source":         "ci",
			}
			if diff := gocmp.Diff("payload", n.Params.Content); diff != "" {
				t.Errorf("content (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(want, n.Params.Meta); diff != "" {
				t.Errorf("meta (-want +got):\n%s", diff)
			}
		})
	}
}

// written records the message_id of every event a Notifier accepted.
type written struct {
	mu  sync.Mutex
	ids []string
}

func (w *written) Notify(_ context.Context, _ string, meta map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ids = append(w.ids, meta[event.KeyMessageID])
	return nil
}

// settled is a settler safe to use from the goroutine of a waiting handle.
type settled struct {
	mu          sync.Mutex
	acks, nacks int
}

func (s *settled) Ack() { s.mu.Lock(); s.acks++; s.mu.Unlock() }

func (s *settled) Nack() { s.mu.Lock(); s.nacks++; s.mu.Unlock() }

// TestHandleRateLimit sends messages through handle with a limiter that grants
// one token an hour, so every message past the burst waits for the whole test.
// Cancelling the context, as shutdown does, must end those waits with a nack
// and no delivery. The outcome does not depend on how fast the test runs: a
// message delivered past the burst shows up as an extra written event, and a
// wait that ignored cancellation would never return.
func TestHandleRateLimit(t *testing.T) {
	tests := map[string]struct {
		limiter   *rate.Limiter
		messages  int
		wantIDs   []string
		wantAcks  int
		wantNacks int
	}{
		"success: zero rate delivers every message without waiting": {
			limiter:  NewLimiter(0, 1),
			messages: 5,
			wantIDs:  []string{"m-0", "m-1", "m-2", "m-3", "m-4"},
			wantAcks: 5,
		},
		"success: a burst is delivered at once": {
			limiter:  rate.NewLimiter(rate.Every(time.Hour), 3),
			messages: 3,
			wantIDs:  []string{"m-0", "m-1", "m-2"},
			wantAcks: 3,
		},
		"error: messages past the burst wait and are nacked when shutdown cancels the wait": {
			limiter:   rate.NewLimiter(rate.Every(time.Hour), 2),
			messages:  4,
			wantIDs:   []string{"m-0", "m-1"},
			wantAcks:  2,
			wantNacks: 2,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var w written
			var s settled
			b := New(&w, event.Options{Subscription: "projects/p/subscriptions/s", MaxContentBytes: event.DefaultMaxContentBytes}, tt.limiter, slog.New(slog.DiscardHandler))

			// Messages are handled one after another, as with an ordering key,
			// so the ones within the burst are the first ones.
			errs := make(chan error, tt.messages)
			go func() {
				for i := range tt.messages {
					errs <- b.handle(ctx, &pubsub.Message{ID: "m-" + strconv.Itoa(i), Data: []byte("x")}, &s)
				}
				close(errs)
			}()
			for range tt.wantAcks {
				select {
				case err := <-errs:
					if err != nil {
						t.Fatalf("handle within the rate = %v, want nil", err)
					}
				case <-time.After(testsupport.LineTimeout):
					t.Fatalf("handle within the rate did not return within %v", testsupport.LineTimeout)
				}
			}

			cancel()
			for err := range errs {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("handle past the burst after cancellation = %v, want %v", err, context.Canceled)
				}
			}

			w.mu.Lock()
			defer w.mu.Unlock()
			if diff := gocmp.Diff(tt.wantIDs, w.ids); diff != "" {
				t.Errorf("written events by message_id (-want +got):\n%s", diff)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if diff := gocmp.Diff([2]int{tt.wantAcks, tt.wantNacks}, [2]int{s.acks, s.nacks}); diff != "" {
				t.Errorf("[acks nacks] (-want +got):\n%s", diff)
			}
		})
	}
}
