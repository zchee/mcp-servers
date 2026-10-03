package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-json-experiment/json"
	gocmp "github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zchee/mcp-servers/pubsub-channel/internal/testsupport"
)

const testInstructions = "test instructions"

// served is a Channel running over pipes with a scripted client.
type served struct {
	ch      *Channel
	client  *testsupport.Client
	runDone chan error
}

// serve starts a Channel over pipes. The test fails if Run has not returned
// shortly after the test ends.
func serve(t *testing.T) *served {
	t.Helper()
	return serveContext(t.Context(), t)
}

// serveContext is serve with Run bound to ctx instead of the test's context.
func serveContext(ctx context.Context, t *testing.T) *served {
	t.Helper()
	client, transport := testsupport.NewClient(t)
	s := &served{
		ch:      New(Options{Name: "pubsub-channel-test", Version: "v0.0.0", Instructions: testInstructions, Logger: slog.New(slog.DiscardHandler)}),
		client:  client,
		runDone: make(chan error, 1),
	}
	go func() { s.runDone <- s.ch.Run(ctx, transport) }()
	t.Cleanup(func() {
		select {
		case <-s.runDone:
		case <-time.After(testsupport.LineTimeout):
			t.Errorf("Channel.Run did not return within %v of the test ending", testsupport.LineTimeout)
		}
	})
	return s
}

// waitInitialized waits until the server has processed
// notifications/initialized, which it does on its own goroutine.
func (s *served) waitInitialized(t *testing.T) {
	t.Helper()
	select {
	case <-s.ch.Initialized():
	case <-time.After(testsupport.LineTimeout):
		t.Fatalf("Initialized() was not closed within %v of notifications/initialized", testsupport.LineTimeout)
	}
}

// handshake completes initialize and notifications/initialized.
func (s *served) handshake(t *testing.T) {
	t.Helper()
	s.client.Initialize(t, "2025-11-25")
	s.client.SendInitialized(t)
	s.waitInitialized(t)
}

func TestProtocolVersions(t *testing.T) {
	tests := map[string]struct {
		version string
		want    bool
	}{
		"success: 2025-11-25 is negotiated": {version: "2025-11-25", want: true},
		"success: 2025-06-18 is negotiated": {version: "2025-06-18", want: true},
		"success: 2026-07-28 is withheld":   {version: "2026-07-28", want: false},
	}
	got := ProtocolVersions()
	if len(got) == 0 {
		t.Fatal("ProtocolVersions() is empty; the server could negotiate nothing")
	}
	for _, v := range got {
		if v >= firstUnregisteredVersion {
			t.Errorf("ProtocolVersions() contains %q, which Claude Code does not register as a channel; full list: %q", v, got)
		}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, slices.Contains(got, tt.version)); diff != "" {
				t.Errorf("ProtocolVersions() = %q contains %s (-want +got):\n%s", got, tt.version, diff)
			}
		})
	}
}

func TestInitialize(t *testing.T) {
	tests := map[string]struct {
		requested string
		want      string
	}{
		"success: initialize requesting 2026-07-28 is answered with 2025-11-25 by the SDK's initialize cap": {
			requested: "2026-07-28",
			want:      "2025-11-25",
		},
		"success: client requesting 2025-11-25 gets 2025-11-25": {
			requested: "2025-11-25",
			want:      "2025-11-25",
		},
		"success: client requesting 2025-06-18 gets 2025-06-18": {
			requested: "2025-06-18",
			want:      "2025-06-18",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := serve(t)
			raw := s.client.Initialize(t, tt.requested)

			var resp map[string]any
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("decode initialize response %q: %v", raw, err)
			}
			if _, ok := resp["error"]; ok {
				t.Fatalf("initialize at %s returned an error: %s", tt.requested, raw)
			}
			result, ok := resp["result"].(map[string]any)
			if !ok {
				t.Fatalf("initialize response has no result object: %s", raw)
			}

			version, _ := result["protocolVersion"].(string)
			if diff := gocmp.Diff(tt.want, version); diff != "" {
				t.Errorf("negotiated protocolVersion for request %s (-want +got):\n%s\nresponse: %s", tt.requested, diff, raw)
			}
			if version >= firstUnregisteredVersion {
				t.Errorf("negotiated protocolVersion %q is not below %s; Claude Code would not register the channel", version, firstUnregisteredVersion)
			}

			wantCaps := map[string]any{"experimental": map[string]any{CapabilityKey: map[string]any{}}}
			if diff := gocmp.Diff(wantCaps, result["capabilities"]); diff != "" {
				t.Errorf("capabilities (-want +got):\n%s\nresponse: %s", diff, raw)
			}
			if diff := gocmp.Diff(testInstructions, result["instructions"]); diff != "" {
				t.Errorf("instructions (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRequestAtUnregisteredVersionIsRejected(t *testing.T) {
	tests := map[string]struct {
		// handshake completes the legacy initialize handshake first.
		handshake bool
		method    string
		version   string
		wantCode  float64
	}{
		"error: server/discover at 2026-07-28 is rejected as unsupported": {
			method:   "server/discover",
			version:  "2026-07-28",
			wantCode: mcp.CodeUnsupportedProtocolVersion,
		},
		"error: request after initialize carrying 2026-07-28 in _meta is rejected as unsupported": {
			handshake: true,
			method:    "tools/list",
			version:   "2026-07-28",
			wantCode:  mcp.CodeUnsupportedProtocolVersion,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := serve(t)
			if tt.handshake {
				s.handshake(t)
			}
			s.client.Send(t, `{"jsonrpc":"2.0","id":7,"method":"`+tt.method+`","params":{"_meta":{"`+mcp.MetaKeyProtocolVersion+`":"`+tt.version+`","`+mcp.MetaKeyClientCapabilities+`":{}}}}`)
			raw := s.client.Next(t, testsupport.LineTimeout)

			var resp struct {
				Error struct {
					Code float64 `json:"code"`
					Data struct {
						Supported []string `json:"supported"`
					} `json:"data"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("decode response %q: %v", raw, err)
			}
			if diff := gocmp.Diff(tt.wantCode, resp.Error.Code); diff != "" {
				t.Errorf("error code (-want +got):\n%s\nresponse: %s", diff, raw)
			}
			if diff := gocmp.Diff(ProtocolVersions(), resp.Error.Data.Supported); diff != "" {
				t.Errorf("advertised versions (-want +got):\n%s\nresponse: %s", diff, raw)
			}
		})
	}
}

func TestNotify(t *testing.T) {
	tests := map[string]struct {
		content string
		meta    map[string]string
		want    string
	}{
		"success: content and meta, meta keys sorted": {
			content: "hello",
			meta:    map[string]string{"subscription": "s", "message_id": "42", "attr_k": "v"},
			want:    `{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"hello","meta":{"attr_k":"v","message_id":"42","subscription":"s"}}}`,
		},
		"success: nil meta omits the member": {
			content: "no meta",
			want:    `{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"no meta"}}`,
		},
		"success: empty content is still a string": {
			content: "",
			meta:    map[string]string{"message_id": "1"},
			want:    `{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"","meta":{"message_id":"1"}}}`,
		},
		"success: newlines and quotes stay on one line": {
			content: "line1\nline2 \"quoted\" <tag> & é",
			meta:    map[string]string{"k": "a\nb"},
			want:    `{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"line1\nline2 \"quoted\" <tag> & é","meta":{"k":"a\nb"}}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := serve(t)
			s.handshake(t)

			if err := s.ch.Notify(t.Context(), tt.content, tt.meta); err != nil {
				t.Fatalf("Notify(%q, %v) = %v, want nil", tt.content, tt.meta, err)
			}
			got := s.client.Next(t, testsupport.LineTimeout)
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("notification bytes (-want +got):\n%s", diff)
			}

			var frame map[string]any
			if err := json.Unmarshal(got, &frame); err != nil {
				t.Fatalf("decode %q: %v", got, err)
			}
			if id, ok := frame["id"]; ok {
				t.Errorf("notification has an id member (%v); Claude Code would treat it as a request: %s", id, got)
			}
			if diff := gocmp.Diff(NotificationMethod, frame["method"]); diff != "" {
				t.Errorf("method (-want +got):\n%s", diff)
			}
			params, ok := frame["params"].(map[string]any)
			if !ok {
				t.Fatalf("params is not an object: %s", got)
			}
			if _, ok := params["content"].(string); !ok {
				t.Errorf("params.content is not a string: %s", got)
			}
			if meta, ok := params["meta"]; ok {
				m, ok := meta.(map[string]any)
				if !ok {
					t.Fatalf("params.meta is not an object: %s", got)
				}
				for k, v := range m {
					if _, ok := v.(string); !ok {
						t.Errorf("params.meta[%q] = %#v, want a string", k, v)
					}
				}
			}

			// One Notify is one line: the next line is the ping response.
			s.client.ExpectPing(t, 100)
		})
	}
}

func TestNotifyBeforeInitialized(t *testing.T) {
	tests := map[string]struct {
		// initialize controls whether the initialize request is sent before the
		// premature Notify.
		initialize bool
	}{
		"error: before any client message":                          {initialize: false},
		"error: after initialize, before notifications/initialized": {initialize: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := serve(t)
			if tt.initialize {
				s.client.Initialize(t, "2025-11-25")
			}

			if err := s.ch.Notify(t.Context(), "too early", nil); !errors.Is(err, ErrNotInitialized) {
				t.Fatalf("Notify before initialized = %v, want %v", err, ErrNotInitialized)
			}

			if !tt.initialize {
				s.client.Initialize(t, "2025-11-25")
			}
			s.client.SendInitialized(t)
			s.waitInitialized(t)
			if err := s.ch.Notify(t.Context(), "marker", nil); err != nil {
				t.Fatalf("Notify after initialized = %v, want nil", err)
			}
			got := s.client.Next(t, testsupport.LineTimeout)
			want := `{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"marker"}}`
			if diff := gocmp.Diff(want, string(got)); diff != "" {
				t.Errorf("first line after the refused Notify is not the marker; the refused call wrote something (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNotifyConcurrent(t *testing.T) {
	tests := map[string]struct {
		senders int
	}{
		"success: one sender":                        {senders: 1},
		"success: 50 senders write whole lines only": {senders: 50},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := serve(t)
			s.handshake(t)

			var wg sync.WaitGroup
			errs := make(chan error, tt.senders)
			for i := range tt.senders {
				wg.Go(func() {
					if err := s.ch.Notify(t.Context(), fmt.Sprintf("event-%02d", i), map[string]string{"seq": strconv.Itoa(i)}); err != nil {
						errs <- fmt.Errorf("Notify #%d: %w", i, err)
					}
				})
			}

			got := make([]string, 0, tt.senders)
			for range tt.senders {
				got = append(got, testsupport.DecodeNotification(t, s.client.Next(t, testsupport.LineTimeout), NotificationMethod).Params.Content)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}

			want := make([]string, 0, tt.senders)
			for i := range tt.senders {
				want = append(want, fmt.Sprintf("event-%02d", i))
			}
			slices.Sort(got)
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("delivered contents (-want +got):\n%s", diff)
			}
			s.client.ExpectPing(t, 200)
		})
	}
}

func TestRunEndsOnClientDisconnect(t *testing.T) {
	tests := map[string]struct {
		// end ends the session from the client side or through Run's context.
		end     func(s *served, cancel context.CancelFunc)
		wantErr error
	}{
		"success: client EOF": {
			end:     func(s *served, _ context.CancelFunc) { s.client.CloseInput() },
			wantErr: nil,
		},
		"success: context cancelled": {
			end:     func(_ *served, cancel context.CancelFunc) { cancel() },
			wantErr: context.Canceled,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s := serveContext(ctx, t)
			s.handshake(t)

			tt.end(s, cancel)
			select {
			case err := <-s.runDone:
				if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
					t.Errorf("Run after the session ended = %v, want %v", err, tt.wantErr)
				}
				s.runDone <- err // let the cleanup observe that Run returned
			case <-time.After(testsupport.LineTimeout):
				t.Fatalf("Run did not return within %v of the session ending", testsupport.LineTimeout)
			}

			if err := s.ch.Notify(t.Context(), "after close", nil); !errors.Is(err, ErrClosed) {
				t.Errorf("Notify after the session ended = %v, want %v", err, ErrClosed)
			}
		})
	}
}
