package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	"github.com/go-json-experiment/json"
	gocmp "github.com/google/go-cmp/cmp"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/zchee/mcp-servers/pubsub-channel/internal/channel"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/event"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/testsupport"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := testsupport.TerminateEmulator(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "terminate Pub/Sub emulator container: %v\n", err)
	}
	os.Exit(code)
}

// envMap returns a getenv backed by env, so tests never read or change the
// process environment.
func envMap(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func TestParseConfig(t *testing.T) {
	const fullName = "projects/my-proj/subscriptions/alerts"
	defaults := config{
		project:         "my-proj",
		subscription:    fullName,
		maxOutstanding:  10,
		maxRate:         1,
		maxBurst:        10,
		maxContentBytes: 256 << 10,
		instructions:    event.Instructions(fullName),
	}
	with := func(edit func(*config)) config {
		c := defaults
		edit(&c)
		return c
	}

	tests := map[string]struct {
		args    []string
		env     map[string]string
		want    config
		wantErr string
	}{
		"success: full subscription name implies the project": {
			args: []string{"-subscription", fullName},
			want: defaults,
		},
		"success: subscription ID with project flag": {
			args: []string{"-project", "my-proj", "-subscription", "alerts"},
			want: defaults,
		},
		"success: environment variables supply project and subscription": {
			env:  map[string]string{envProject: "my-proj", envSubscription: "alerts"},
			want: defaults,
		},
		"success: flags override environment variables": {
			args: []string{"-subscription", fullName},
			env:  map[string]string{envSubscription: "projects/other/subscriptions/other-sub"},
			want: defaults,
		},
		"success: matching project flag and full name": {
			args: []string{"-project", "my-proj", "-subscription", fullName},
			want: defaults,
		},
		"success: attributes list is split and trimmed": {
			args: []string{"-subscription", fullName, "-attributes", " event.type, repo/name ,,"},
			want: with(func(c *config) { c.attributes = []string{"event.type", "repo/name"} }),
		},
		"success: limits and instructions are taken from flags": {
			args: []string{"-subscription", fullName, "-max-outstanding", "3", "-max-content-bytes", "4096", "-instructions", "custom"},
			want: with(func(c *config) {
				c.maxOutstanding = 3
				c.maxContentBytes = 4096
				c.instructions = "custom"
			}),
		},
		"success: rate limit is taken from flags": {
			args: []string{"-subscription", fullName, "-max-rate", "0.5", "-max-burst", "3"},
			want: with(func(c *config) {
				c.maxRate = 0.5
				c.maxBurst = 3
			}),
		},
		"success: rate limit is taken from environment variables": {
			args: []string{"-subscription", fullName},
			env:  map[string]string{envMaxRate: "2.5", envMaxBurst: "4"},
			want: with(func(c *config) {
				c.maxRate = 2.5
				c.maxBurst = 4
			}),
		},
		"success: rate limit flags override environment variables": {
			args: []string{"-subscription", fullName, "-max-rate", "3", "-max-burst", "5"},
			env:  map[string]string{envMaxRate: "2.5", envMaxBurst: "4"},
			want: with(func(c *config) {
				c.maxRate = 3
				c.maxBurst = 5
			}),
		},
		"success: max rate flag overrides a malformed environment value": {
			args: []string{"-subscription", fullName, "-max-rate", "3"},
			env:  map[string]string{envMaxRate: "fast"},
			want: with(func(c *config) { c.maxRate = 3 }),
		},
		"success: max burst flag overrides a malformed environment value": {
			args: []string{"-subscription", fullName, "-max-burst", "5"},
			env:  map[string]string{envMaxBurst: "1.5"},
			want: with(func(c *config) { c.maxBurst = 5 }),
		},
		"success: zero rate disables the limit": {
			args: []string{"-subscription", fullName, "-max-rate", "0"},
			want: with(func(c *config) { c.maxRate = 0 }),
		},
		"success: minimum max content bytes": {
			args: []string{"-subscription", fullName, "-max-content-bytes", "4"},
			want: with(func(c *config) { c.maxContentBytes = 4 }),
		},
		"success: legacy domain-scoped project": {
			args: []string{"-subscription", "projects/example.com:my-proj/subscriptions/alerts"},
			want: with(func(c *config) {
				c.project = "example.com:my-proj"
				c.subscription = "projects/example.com:my-proj/subscriptions/alerts"
				c.instructions = event.Instructions(c.subscription)
			}),
		},
		"success: numeric project number": {
			args: []string{"-project", "123456789012", "-subscription", "alerts"},
			want: with(func(c *config) {
				c.project = "123456789012"
				c.subscription = "projects/123456789012/subscriptions/alerts"
				c.instructions = event.Instructions(c.subscription)
			}),
		},
		"success: short project in the full name with the emulator": {
			args: []string{"-subscription", "projects/local/subscriptions/alerts"},
			env:  map[string]string{envEmulatorHost: "localhost:8085"},
			want: with(func(c *config) {
				c.project = "local"
				c.subscription = "projects/local/subscriptions/alerts"
				c.instructions = event.Instructions(c.subscription)
			}),
		},
		"success: one-letter project flag with the emulator": {
			args: []string{"-project", "p", "-subscription", "alerts"},
			env:  map[string]string{envEmulatorHost: "localhost:8085"},
			want: with(func(c *config) {
				c.project = "p"
				c.subscription = "projects/p/subscriptions/alerts"
				c.instructions = event.Instructions(c.subscription)
			}),
		},
		"error: missing subscription": {
			wantErr: "a subscription is required",
		},
		"error: invalid project with a subscription ID": {
			args:    []string{"-project", "a/b", "-subscription", "alerts"},
			wantErr: `project "a/b" is not a valid Google Cloud project ID`,
		},
		"error: project with uppercase letters": {
			args:    []string{"-project", "My-Proj", "-subscription", "alerts"},
			wantErr: "is not a valid Google Cloud project ID",
		},
		"error: invalid project inside the full name": {
			args:    []string{"-subscription", "projects/p/subscriptions/alerts"},
			wantErr: `project "p" is not a valid Google Cloud project ID`,
		},
		"error: short project without the emulator": {
			args:    []string{"-subscription", "projects/local/subscriptions/alerts"},
			wantErr: `project "local" is not a valid Google Cloud project ID`,
		},
		"error: invalid subscription ID is still rejected with the emulator": {
			args:    []string{"-subscription", "projects/local/subscriptions/1alerts"},
			env:     map[string]string{envEmulatorHost: "localhost:8085"},
			wantErr: "not a valid Pub/Sub subscription ID",
		},
		"error: subscription ID with the reserved goog prefix": {
			args:    []string{"-project", "my-proj", "-subscription", "goog-alerts"},
			wantErr: "not a valid Pub/Sub subscription ID",
		},
		"error: attributes naming no key": {
			args:    []string{"-subscription", fullName, "-attributes", ","},
			wantErr: "names no attribute key",
		},
		"error: attributes of only blanks and commas": {
			args:    []string{"-subscription", fullName, "-attributes", " , "},
			wantErr: "names no attribute key",
		},
		"error: negative max rate": {
			args:    []string{"-subscription", fullName, "-max-rate", "-1"},
			wantErr: "-max-rate must be a finite number",
		},
		"error: NaN max rate": {
			args:    []string{"-subscription", fullName, "-max-rate", "NaN"},
			wantErr: "-max-rate must be a finite number",
		},
		"error: infinite max rate": {
			args:    []string{"-subscription", fullName, "-max-rate", "Inf"},
			wantErr: "-max-rate must be a finite number",
		},
		"error: zero max burst": {
			args:    []string{"-subscription", fullName, "-max-burst", "0"},
			wantErr: "-max-burst must be at least 1",
		},
		"error: max rate environment variable is not a number": {
			args:    []string{"-subscription", fullName},
			env:     map[string]string{envMaxRate: "fast"},
			wantErr: `PUBSUB_CHANNEL_MAX_RATE="fast" is not a number`,
		},
		"error: max burst environment variable is not an integer": {
			args:    []string{"-subscription", fullName},
			env:     map[string]string{envMaxBurst: "1.5"},
			wantErr: `PUBSUB_CHANNEL_MAX_BURST="1.5" is not an integer`,
		},
		"error: subscription ID without a project": {
			args:    []string{"-subscription", "alerts"},
			wantErr: "a project is required",
		},
		"error: project flag conflicts with the full name": {
			args:    []string{"-project", "other", "-subscription", fullName},
			wantErr: "conflicts with the project",
		},
		"error: malformed resource name": {
			args:    []string{"-subscription", "projects/my-proj/topics/alerts"},
			wantErr: "neither an ID nor a name",
		},
		"error: invalid subscription ID": {
			args:    []string{"-project", "my-proj", "-subscription", "1alerts"},
			wantErr: "not a valid Pub/Sub subscription ID",
		},
		"error: unexpanded variable from .mcp.json": {
			env:     map[string]string{envSubscription: "${PUBSUB_CHANNEL_SUBSCRIPTION}"},
			wantErr: "unexpanded ${VAR} reference",
		},
		"error: zero max outstanding": {
			args:    []string{"-subscription", fullName, "-max-outstanding", "0"},
			wantErr: "-max-outstanding must be at least 1",
		},
		"error: max content bytes too small to keep any binary data": {
			args:    []string{"-subscription", fullName, "-max-content-bytes", "3"},
			wantErr: "-max-content-bytes must be at least 4, got 3",
		},
		"error: positional arguments": {
			args:    []string{"-subscription", fullName, "extra"},
			wantErr: "unexpected arguments",
		},
		"error: unknown flag": {
			args:    []string{"-key-file", "creds.json"},
			wantErr: "flag provided but not defined",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseConfig(tt.args, envMap(tt.env), &stderr)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseConfig(%q) error = %v, want one containing %q", tt.args, err, tt.wantErr)
				}
				if !strings.Contains(stderr.String(), "Usage: pubsub-channel") {
					t.Errorf("parseConfig(%q) did not print the usage on error; stderr:\n%s", tt.args, stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig(%q) = %v, want nil; stderr:\n%s", tt.args, err, stderr.String())
			}
			if diff := gocmp.Diff(tt.want, got, gocmp.AllowUnexported(config{})); diff != "" {
				t.Errorf("parseConfig(%q) (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a logger and
// the reads of a test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunConfigurationError(t *testing.T) {
	tests := map[string]struct {
		args     []string
		env      map[string]string
		wantCode int
		wantLast string
	}{
		"error: missing subscription exits with usage": {
			wantCode: exitUsage,
			wantLast: "pubsub-channel: a subscription is required: set -subscription or PUBSUB_CHANNEL_SUBSCRIPTION",
		},
		"error: unexpanded variable exits with usage": {
			env:      map[string]string{envSubscription: "${PUBSUB_CHANNEL_SUBSCRIPTION}"},
			wantCode: exitUsage,
			wantLast: `pubsub-channel: subscription "${PUBSUB_CHANNEL_SUBSCRIPTION}" looks like an unexpanded ${VAR} reference; set the variable in the environment Claude Code runs in`,
		},
		"error: invalid project exits with usage": {
			args:     []string{"-project", "a/b", "-subscription", "alerts"},
			wantCode: exitUsage,
			wantLast: `pubsub-channel: project "a/b" is not a valid Google Cloud project ID`,
		},
		"error: malformed max rate environment value exits with usage": {
			args:     []string{"-subscription", "projects/my-proj/subscriptions/alerts"},
			env:      map[string]string{envMaxRate: "fast"},
			wantCode: exitUsage,
			wantLast: `pubsub-channel: PUBSUB_CHANNEL_MAX_RATE="fast" is not a number`,
		},
		"success: -help exits zero": {
			args:     []string{"-help"},
			wantCode: exitOK,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client, transport := testsupport.NewClient(t)
			var stderr syncBuffer

			code := run(t.Context(), tt.args, envMap(tt.env), transport, &stderr)

			if code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d; stderr:\n%s", tt.args, code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Usage: pubsub-channel") {
				t.Errorf("stderr has no usage message:\n%s", stderr.String())
			}
			if tt.wantLast != "" {
				lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
				if diff := gocmp.Diff(tt.wantLast, lines[len(lines)-1]); diff != "" {
					t.Errorf("last stderr line (-want +got):\n%s", diff)
				}
			}
			// Nothing reaches the protocol stream.
			client.ExpectSilence(t, 100*time.Millisecond)
		})
	}
}

// started is a run of the whole server against the Pub/Sub emulator.
type started struct {
	client *testsupport.Client
	stderr *syncBuffer
	cancel context.CancelFunc
	code   chan int
}

// startRun runs the server with args against the emulator, under a context
// the test can cancel to simulate SIGINT or SIGTERM.
func startRun(t *testing.T, args []string) *started {
	t.Helper()
	_, opts := testsupport.EmulatorProject(t)
	client, transport := testsupport.NewClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	s := &started{client: client, stderr: &syncBuffer{}, cancel: cancel, code: make(chan int, 1)}
	go func() { s.code <- run(ctx, args, envMap(nil), transport, s.stderr, opts...) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.code:
		case <-time.After(15 * time.Second):
			t.Errorf("run did not return within 15s of the test ending; stderr:\n%s", s.stderr.String())
		}
	})
	return s
}

// exitCode waits for run to return.
func (s *started) exitCode(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-s.code:
		s.code <- code // let the cleanup observe that run returned
		return code
	case <-time.After(timeout):
		t.Fatalf("run did not return within %v; stderr:\n%s", timeout, s.stderr.String())
		return -1
	}
}

// handshake plays initialize and notifications/initialized and returns the
// decoded initialize result.
func (s *started) handshake(t *testing.T) map[string]any {
	t.Helper()
	raw := s.client.Initialize(t, "2026-07-28")
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Result == nil {
		t.Fatalf("initialize response %q: %v", raw, err)
	}
	s.client.SendInitialized(t)
	return resp.Result
}

// TestRunAgainstEmulator runs the whole server against the Pub/Sub emulator
// and checks the handshake, delivery, and how each way of ending the session
// maps to an exit code and stderr.
func TestRunAgainstEmulator(t *testing.T) {
	tests := map[string]struct {
		// missingSubscription points the server at a subscription that does
		// not exist.
		missingSubscription bool
		extraArgs           []string
		// message is published after the handshake; nil publishes nothing.
		message     *pubsub.Message
		wantContent string
		wantMeta    func(id, subscription string) map[string]string
		// shutdown ends the session; nil waits for the server to exit by itself.
		shutdown func(s *started)
		wantCode int
		// wantLastLine, when set, checks the last line on stderr.
		wantLastLine func(t *testing.T, subscription, line string)
	}{
		"success: delivers with the attribute allowlist and exits 0 when the client closes stdin": {
			extraArgs:   []string{"-attributes", "kind"},
			message:     &pubsub.Message{Data: []byte("deploy done"), Attributes: map[string]string{"kind": "deploy", "secret": "not forwarded"}},
			wantContent: "deploy done",
			wantMeta: func(id, subscription string) map[string]string {
				return map[string]string{event.KeyMessageID: id, event.KeySubscription: subscription, "attr_kind": "deploy"}
			},
			shutdown: func(s *started) { s.client.CloseInput() },
			wantCode: exitOK,
		},
		"success: exits 0 on SIGINT or SIGTERM": {
			message:     &pubsub.Message{Data: []byte("warm-up")},
			wantContent: "warm-up",
			wantMeta: func(id, subscription string) map[string]string {
				return map[string]string{event.KeyMessageID: id, event.KeySubscription: subscription}
			},
			// signal.NotifyContext cancels the context on SIGINT or SIGTERM.
			shutdown: func(s *started) { s.cancel() },
			wantCode: exitOK,
		},
		"error: missing subscription exits 1 with one line naming it": {
			missingSubscription: true,
			wantCode:            exitFailure,
			wantLastLine: func(t *testing.T, subscription, line string) {
				t.Helper()
				if !strings.HasPrefix(line, "pubsub-channel: receive from "+subscription+":") || !strings.Contains(line, "NotFound") {
					t.Errorf("last stderr line = %q, want the receive error naming %s with NotFound", line, subscription)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := testsupport.EmulatorClient(t)
			var f testsupport.Fixture
			if tt.missingSubscription {
				f = testsupport.Fixture{Client: client, Subscription: fmt.Sprintf("projects/%s/subscriptions/does-not-exist-%d", client.Project(), time.Now().UnixNano())}
			} else {
				f = testsupport.NewFixture(t, client)
			}
			s := startRun(t, append([]string{"-subscription", f.Subscription}, tt.extraArgs...))

			result := s.handshake(t)
			if v, ok := result["protocolVersion"].(string); !ok {
				t.Errorf("initialize result has no string protocolVersion: %v", result)
			} else if v >= "2026-07-28" {
				t.Errorf("negotiated protocolVersion %q, want one below 2026-07-28", v)
			}
			if diff := gocmp.Diff(event.Instructions(f.Subscription), result["instructions"]); diff != "" {
				t.Errorf("instructions (-want +got):\n%s", diff)
			}

			if tt.message != nil {
				id := f.Publish(t, tt.message)
				n := testsupport.DecodeNotification(t, s.client.Next(t, testsupport.DeliveryTimeout), channel.NotificationMethod)
				if diff := gocmp.Diff(tt.wantContent, n.Params.Content); diff != "" {
					t.Errorf("content (-want +got):\n%s", diff)
				}
				delete(n.Params.Meta, event.KeyPublishTime)
				if diff := gocmp.Diff(tt.wantMeta(id, f.Subscription), n.Params.Meta); diff != "" {
					t.Errorf("meta (-want +got):\n%s", diff)
				}
			}

			if tt.shutdown != nil {
				tt.shutdown(s)
			}
			if code := s.exitCode(t, 30*time.Second); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, s.stderr.String())
			}
			if tt.wantLastLine != nil {
				lines := strings.Split(strings.TrimRight(s.stderr.String(), "\n"), "\n")
				tt.wantLastLine(t, f.Subscription, lines[len(lines)-1])
			}
		})
	}
}

// shutdownBound is how soon run must return after the session ends, in every
// state of the Pub/Sub connection.
const shutdownBound = 5 * time.Second

// TestRunExitsWhilePubSubUnreachable points the server at a closed local port,
// so the pull stream never connects and keeps retrying, and checks that each
// way of ending the session still makes run return promptly. It needs no
// container runtime.
func TestRunExitsWhilePubSubUnreachable(t *testing.T) {
	tests := map[string]struct {
		// shutdown ends the session.
		shutdown func(s *started)
	}{
		"success: client EOF": {
			shutdown: func(s *started) { s.client.CloseInput() },
		},
		"success: context cancelled": {
			// signal.NotifyContext cancels the context on SIGINT or SIGTERM.
			shutdown: func(s *started) { s.cancel() },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, transport := testsupport.NewClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			s := &started{client: client, stderr: &syncBuffer{}, cancel: cancel, code: make(chan int, 1)}
			// Port 1 on the loopback address is closed, so every connection
			// attempt is refused at once.
			opts := []option.ClientOption{
				option.WithEndpoint("127.0.0.1:1"),
				option.WithoutAuthentication(),
				option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
			}
			go func() {
				s.code <- run(ctx, []string{"-subscription", "projects/test-project/subscriptions/unreachable"}, envMap(nil), transport, s.stderr, opts...)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-s.code:
				case <-time.After(15 * time.Second):
					t.Errorf("run did not return within 15s of the test ending; stderr:\n%s", s.stderr.String())
				}
			})

			s.handshake(t)
			// Receiving starts after the handshake; end the session only once
			// it has, so the shutdown meets a pull stream that is retrying.
			deadline := time.Now().Add(testsupport.LineTimeout)
			for !strings.Contains(s.stderr.String(), "session initialized; receiving") {
				if time.Now().After(deadline) {
					t.Fatalf("server did not start receiving within %v; stderr:\n%s", testsupport.LineTimeout, s.stderr.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(500 * time.Millisecond) // let the first connection attempts fail

			start := time.Now()
			tt.shutdown(s)
			code := s.exitCode(t, shutdownBound)
			t.Logf("run returned %v after the session ended", time.Since(start))
			if code != exitOK {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, exitOK, s.stderr.String())
			}
		})
	}
}
