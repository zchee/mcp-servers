package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/mcp-servers/pubsub-channel/internal/event"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/testsupport"
)

// cancelBound is how quickly Receive must return once its context is
// cancelled.
const cancelBound = 5 * time.Second

func TestMain(m *testing.M) {
	code := m.Run()
	if err := testsupport.TerminateEmulator(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "terminate Pub/Sub emulator container: %v\n", err)
	}
	os.Exit(code)
}

// runBridge runs a Bridge on the fixture's subscription until the returned
// stop function is called. stop returns how long Run took to return after
// cancellation and Run's error.
func runBridge(t *testing.T, f testsupport.Fixture, n Notifier) (stop func() (time.Duration, error)) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	sub := f.Client.Subscriber(f.Subscription)
	sub.ReceiveSettings.MaxOutstandingMessages = DefaultMaxOutstandingMessages
	b := New(n, event.Options{Subscription: f.Subscription, MaxContentBytes: event.DefaultMaxContentBytes}, NewLimiter(0, 1), slog.New(slog.DiscardHandler))

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx, sub) }()

	var once sync.Once
	var took time.Duration
	var err error
	stop = func() (time.Duration, error) {
		once.Do(func() {
			start := time.Now()
			cancel()
			select {
			case err = <-done:
			case <-time.After(cancelBound + 10*time.Second):
				err = errors.New("Bridge.Run did not return after cancellation")
			}
			took = time.Since(start)
		})
		return took, err
	}
	t.Cleanup(func() {
		if _, err := stop(); err != nil {
			t.Errorf("stop bridge: %v", err)
		}
	})
	return stop
}

// expectNoRedelivery receives from the fixture's subscription with a plain
// subscriber for longer than the ack deadline and fails on any message: a
// message the bridge had not acked would be redelivered within that window.
func expectNoRedelivery(t *testing.T, f testsupport.Fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testsupport.AckDeadline+5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got []string
	err := f.Client.Subscriber(f.Subscription).Receive(ctx, func(_ context.Context, m *pubsub.Message) {
		mu.Lock()
		got = append(got, m.ID)
		mu.Unlock()
		m.Ack()
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("observation Receive on %s: %v", f.Subscription, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) > 0 {
		t.Errorf("messages %q were redelivered after the bridge acked them", got)
	}
}

// attempts passes deliveries to next and records each attempt. With failFirst
// set, the first delivery fails inside the real channel: it is handed an
// already-cancelled context, so the transport refuses the write before any
// byte is written and Notify returns the context's error.
type attempts struct {
	next      Notifier
	failFirst bool

	mu    sync.Mutex
	calls []string // message_id of every delivery attempt
	errs  []error
}

func (f *attempts) Notify(ctx context.Context, content string, meta map[string]string) error {
	f.mu.Lock()
	first := len(f.calls) == 0
	f.calls = append(f.calls, meta[event.KeyMessageID])
	f.mu.Unlock()

	if first && f.failFirst {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		ctx = cctx
	}
	err := f.next.Notify(ctx, content, meta)
	f.mu.Lock()
	f.errs = append(f.errs, err)
	f.mu.Unlock()
	return err
}

// TestEmulator publishes one message to a subscription in the Pub/Sub emulator
// and checks, for each case, that it reaches the client as exactly one
// notification, that it is acked (not redelivered after the bridge stops),
// and that Receive returns promptly once cancelled.
func TestEmulator(t *testing.T) {
	tests := map[string]struct {
		message *pubsub.Message
		// failFirstWrite makes the first delivery attempt fail.
		failFirstWrite bool
		wantContent    string
		// wantMeta returns the expected meta without publish_time, which is
		// checked separately.
		wantMeta func(id, subscription string) map[string]string
		// wantErrs is the outcome of each delivery attempt, in order: nil for a
		// written notification, the cause for a failed one.
		wantErrs []error
	}{
		"success: delivered once and acked, with attributes and ordering key in meta": {
			message: &pubsub.Message{
				Data:        []byte("build 1234 failed on main"),
				Attributes:  map[string]string{"event.type": "build", "repo/name": "zchee/mcp-servers"},
				OrderingKey: "repo-mcp-servers",
			},
			wantContent: "build 1234 failed on main",
			wantMeta: func(id, subscription string) map[string]string {
				return map[string]string{
					event.KeyMessageID:    id,
					event.KeySubscription: subscription,
					event.KeyOrderingKey:  "repo-mcp-servers",
					"attr_event_type":     "build",
					"attr_repo_name":      "zchee/mcp-servers",
				}
			},
			wantErrs: []error{nil},
		},
		"error: failed write is nacked and the redelivery is delivered once": {
			message:        &pubsub.Message{Data: []byte("retry me")},
			failFirstWrite: true,
			wantContent:    "retry me",
			wantMeta: func(id, subscription string) map[string]string {
				return map[string]string{event.KeyMessageID: id, event.KeySubscription: subscription}
			},
			wantErrs: []error{context.Canceled, nil},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := testsupport.NewFixture(t, testsupport.EmulatorClient(t))
			s := startSession(t)
			s.handshake(t)
			n := &attempts{next: s.ch, failFirst: tt.failFirstWrite}
			stop := runBridge(t, f, n)

			id := f.Publish(t, tt.message)

			got := s.next(t, testsupport.DeliveryTimeout)
			if diff := gocmp.Diff(tt.wantContent, got.Params.Content); diff != "" {
				t.Errorf("content (-want +got):\n%s", diff)
			}
			// publish_time comes from the emulator's clock, which can drift far
			// from the host's (a container VM falls behind while the host
			// sleeps), so the window only rules out a zero or garbage time.
			publishTime, err := time.Parse(time.RFC3339Nano, got.Params.Meta[event.KeyPublishTime])
			if err != nil {
				t.Errorf("meta publish_time %q is not RFC 3339: %v", got.Params.Meta[event.KeyPublishTime], err)
			}
			if age := time.Since(publishTime); age < -24*time.Hour || age > 24*time.Hour {
				t.Errorf("meta publish_time %v is %v away from now, more than any clock drift explains", publishTime, age)
			}
			delete(got.Params.Meta, event.KeyPublishTime)
			if diff := gocmp.Diff(tt.wantMeta(id, f.Subscription), got.Params.Meta); diff != "" {
				t.Errorf("meta (-want +got):\n%s", diff)
			}

			// Exactly one notification: nothing else arrives while the bridge runs.
			s.client.ExpectSilence(t, 3*time.Second)

			took, err := stop()
			if err != nil {
				t.Fatalf("Bridge.Run = %v, want nil after cancellation", err)
			}
			if took > cancelBound {
				t.Errorf("Bridge.Run returned %v after cancellation, want at most %v", took, cancelBound)
			}
			t.Logf("Bridge.Run returned %v after cancellation", took)

			n.mu.Lock()
			wantCalls := make([]string, len(tt.wantErrs))
			for i := range wantCalls {
				wantCalls[i] = id
			}
			if diff := gocmp.Diff(wantCalls, n.calls); diff != "" {
				t.Errorf("delivery attempts by message_id (-want +got):\n%s", diff)
			}
			if len(n.errs) != len(tt.wantErrs) {
				t.Errorf("delivery attempt errors = %v, want %v", n.errs, tt.wantErrs)
			} else {
				for i, want := range tt.wantErrs {
					if !errors.Is(n.errs[i], want) || (want == nil && n.errs[i] != nil) {
						t.Errorf("delivery attempt #%d error = %v, want %v", i+1, n.errs[i], want)
					}
				}
			}
			n.mu.Unlock()

			expectNoRedelivery(t, f)
		})
	}
}

// TestRealSubscription delivers one message through a real Pub/Sub topic and
// subscription. The subscription must be dedicated to this test: the bridge
// acks every message it delivers, including ones other publishers sent.
func TestRealSubscription(t *testing.T) {
	tests := map[string]struct {
		data []byte
	}{
		"success: published message is delivered with its ID and subscription": {
			data: []byte("pubsub-channel integration test"),
		},
	}
	project := os.Getenv("PUBSUB_CHANNEL_TEST_PROJECT")
	subscription := os.Getenv("PUBSUB_CHANNEL_TEST_SUBSCRIPTION")
	topic := os.Getenv("PUBSUB_CHANNEL_TEST_TOPIC")
	if project == "" || subscription == "" || topic == "" {
		t.Skip("set PUBSUB_CHANNEL_TEST_PROJECT, PUBSUB_CHANNEL_TEST_SUBSCRIPTION and PUBSUB_CHANNEL_TEST_TOPIC (a topic and a dedicated subscription on it, with Application Default Credentials) to run against real Pub/Sub")
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client, err := pubsub.NewClient(t.Context(), project)
			if err != nil {
				t.Fatalf("connect to Pub/Sub project %s: %v", project, err)
			}
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Errorf("close Pub/Sub client: %v", err)
				}
			})
			f := testsupport.Fixture{Client: client, Topic: topic, Subscription: subscription}

			s := startSession(t)
			s.handshake(t)
			stop := runBridge(t, f, s.ch)

			runID := strconv.FormatInt(time.Now().UnixNano(), 10)
			id := f.Publish(t, &pubsub.Message{Data: tt.data, Attributes: map[string]string{"test_run": runID}})

			deadline := time.Now().Add(2 * testsupport.DeliveryTimeout)
			for {
				remaining := time.Until(deadline)
				if remaining <= 0 {
					t.Fatalf("message %s was not delivered within %v", id, 2*testsupport.DeliveryTimeout)
				}
				n := s.next(t, remaining)
				if n.Params.Meta["attr_test_run"] != runID {
					t.Logf("ignoring a message from another publisher: message_id=%s", n.Params.Meta[event.KeyMessageID])
					continue
				}
				if diff := gocmp.Diff(id, n.Params.Meta[event.KeyMessageID]); diff != "" {
					t.Errorf("message_id (-want +got):\n%s", diff)
				}
				if diff := gocmp.Diff(subscription, n.Params.Meta[event.KeySubscription]); diff != "" {
					t.Errorf("meta subscription (-want +got):\n%s", diff)
				}
				if diff := gocmp.Diff(string(tt.data), n.Params.Content); diff != "" {
					t.Errorf("content (-want +got):\n%s", diff)
				}
				break
			}
			if _, err := stop(); err != nil {
				t.Fatalf("Bridge.Run = %v, want nil after cancellation", err)
			}
		})
	}
}
