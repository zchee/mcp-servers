package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/zchee/mcp-servers/pubsub-channel/internal/lock"
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
		"success: receiving is disabled by default": {
			args: []string{"-subscription", fullName},
			want: defaults,
		},
		"success: enable environment variable turns receiving on": {
			args: []string{"-subscription", fullName},
			env:  map[string]string{envEnable: "1"},
			want: with(func(c *config) { c.enable = true }),
		},
		"success: enable environment variable false keeps receiving off": {
			args: []string{"-subscription", fullName},
			env:  map[string]string{envEnable: "false"},
			want: defaults,
		},
		"success: enable flag turns receiving on": {
			args: []string{"-subscription", fullName, "-enable"},
			want: with(func(c *config) { c.enable = true }),
		},
		"success: enable flag false overrides the environment variable": {
			args: []string{"-subscription", fullName, "-enable=false"},
			env:  map[string]string{envEnable: "true"},
			want: defaults,
		},
		"success: enable flag overrides a malformed environment value": {
			args: []string{"-subscription", fullName, "-enable"},
			env:  map[string]string{envEnable: "yes"},
			want: with(func(c *config) { c.enable = true }),
		},
		"success: malformed enable environment value keeps receiving off with a warning": {
			args: []string{"-subscription", fullName},
			env:  map[string]string{envEnable: "yes"},
			want: with(func(c *config) {
				c.enableWarning = `PUBSUB_CHANNEL_ENABLE="yes" is not a boolean; receiving stays disabled`
			}),
		},
		"success: unexpanded enable placeholder keeps receiving off with a warning": {
			args: []string{"-subscription", fullName},
			env:  map[string]string{envEnable: "${PUBSUB_CHANNEL_ENABLE:-}"},
			want: with(func(c *config) {
				c.enableWarning = `PUBSUB_CHANNEL_ENABLE="${PUBSUB_CHANNEL_ENABLE:-}" is not a boolean; receiving stays disabled`
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

			code := run(t.Context(), tt.args, envMap(tt.env), transport, &stderr, t.TempDir())

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

// started is a run of the whole server.
type started struct {
	client *testsupport.Client
	stderr *syncBuffer
	cancel context.CancelFunc
	code   chan int
}

// startServer runs the server with args, env and lockDir, connecting to Pub/Sub
// with opts, under a context the test can cancel to simulate SIGINT or SIGTERM.
// lockDir must belong to the test, so that no test touches the lock files of
// servers running outside it.
func startServer(t *testing.T, args []string, env map[string]string, lockDir string, opts []option.ClientOption) *started {
	t.Helper()
	// An empty lockDir selects the real default lock directory.
	if lockDir == "" {
		t.Fatal("startServer needs a lock directory that belongs to the test")
	}
	client, transport := testsupport.NewClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	s := &started{client: client, stderr: &syncBuffer{}, cancel: cancel, code: make(chan int, 1)}
	go func() { s.code <- run(ctx, args, envMap(env), transport, s.stderr, lockDir, opts...) }()
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

// startRun runs the server with args and lockDir against the emulator.
func startRun(t *testing.T, args []string, lockDir string) *started {
	t.Helper()
	_, opts := testsupport.EmulatorProject(t)
	return startServer(t, args, nil, lockDir, opts)
}

// waitForLog waits until stderr contains substr.
func (s *started) waitForLog(t *testing.T, substr string) {
	t.Helper()
	deadline := time.Now().Add(testsupport.LineTimeout)
	for !strings.Contains(s.stderr.String(), substr) {
		if time.Now().After(deadline) {
			t.Fatalf("stderr has no %q within %v; stderr:\n%s", substr, testsupport.LineTimeout, s.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
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
			s := startRun(t, append([]string{"-enable", "-subscription", f.Subscription}, tt.extraArgs...), t.TempDir())

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
			// Port 1 on the loopback address is closed, so every connection
			// attempt is refused at once.
			opts := []option.ClientOption{
				option.WithEndpoint("127.0.0.1:1"),
				option.WithoutAuthentication(),
				option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
			}
			s := startServer(t, []string{"-enable", "-subscription", "projects/test-project/subscriptions/unreachable"}, nil, t.TempDir(), opts)

			s.handshake(t)
			// Receiving starts after the handshake; end the session only once
			// it has, so the shutdown meets a pull stream that is retrying.
			s.waitForLog(t, "session initialized; receiving")
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

// countingEndpoint listens on a loopback port, closes every connection made to
// it at once, and returns client options that point Pub/Sub at it together with
// the number of connections accepted so far.
func countingEndpoint(t *testing.T) ([]option.ClientOption, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on loopback: %v", err)
	}
	var accepted atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // the listener is closed when the test ends
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return []option.ClientOption{
		option.WithEndpoint(ln.Addr().String()),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}, &accepted
}

// TestRunIdle checks every way the server ends up idle: it completes the
// handshake with instructions saying no events will arrive, logs why, never
// connects to Pub/Sub, writes nothing after the handshake, and exits 0 when the
// client closes stdin. The last case is the control: an enabled server with a
// free lock does connect, which shows that the connection counter can see the
// connections the idle cases must not make. It needs no container runtime.
func TestRunIdle(t *testing.T) {
	const subscription = "projects/test-project/subscriptions/idle"

	tests := map[string]struct {
		args []string
		env  map[string]string
		// holdLock takes the subscription lock in the server's lock directory
		// before the server starts, as another process would.
		holdLock bool
		// wantLogs must each appear exactly once on stderr.
		wantLogs []string
		// wantReceiving expects the server to receive instead of staying idle.
		wantReceiving bool
	}{
		"success: receiving is disabled by default": {
			args:     []string{"-subscription", subscription},
			wantLogs: []string{"receiving is disabled; to receive, set PUBSUB_CHANNEL_ENABLE=1 or pass -enable"},
		},
		"success: an unexpanded enable placeholder is reported once and leaves the server idle": {
			args: []string{"-subscription", subscription},
			env:  map[string]string{envEnable: "${PUBSUB_CHANNEL_ENABLE:-}"},
			wantLogs: []string{
				`PUBSUB_CHANNEL_ENABLE=\"${PUBSUB_CHANNEL_ENABLE:-}\" is not a boolean; receiving stays disabled`,
				"receiving is disabled",
			},
		},
		"success: custom instructions are replaced while idle": {
			args:     []string{"-subscription", subscription, "-instructions", "events are coming"},
			wantLogs: []string{"receiving is disabled"},
		},
		"success: enabled while another process holds the lock": {
			args:     []string{"-enable", "-subscription", subscription},
			holdLock: true,
			wantLogs: []string{
				"another pubsub-channel process on this machine is receiving from " + subscription + "; this server stays idle for its lifetime",
				"holder=\"pid=" + fmt.Sprint(os.Getpid()) + " started=",
			},
		},
		"success: enabled with a free lock connects to Pub/Sub": {
			args:          []string{"-enable", "-subscription", subscription},
			wantLogs:      []string{"session initialized; receiving"},
			wantReceiving: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lockDir := t.TempDir()
			if tt.holdLock {
				l, err := lock.Acquire(lockDir, subscription)
				if err != nil {
					t.Fatalf("hold the subscription lock: %v", err)
				}
				if err := l.WriteHolder(time.Now(), subscription); err != nil {
					t.Fatalf("record the lock holder: %v", err)
				}
				t.Cleanup(func() { _ = l.Release() })
			}
			opts, accepted := countingEndpoint(t)
			s := startServer(t, tt.args, tt.env, lockDir, opts)

			result := s.handshake(t)
			for _, want := range tt.wantLogs {
				s.waitForLog(t, want)
			}

			if tt.wantReceiving {
				deadline := time.Now().Add(testsupport.LineTimeout)
				for accepted.Load() == 0 {
					if time.Now().After(deadline) {
						t.Fatalf("enabled server made no connection to Pub/Sub within %v; stderr:\n%s", testsupport.LineTimeout, s.stderr.String())
					}
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				if diff := gocmp.Diff(event.InactiveInstructions(subscription), result["instructions"]); diff != "" {
					t.Errorf("instructions (-want +got):\n%s", diff)
				}
				// No notification, or anything else, follows the handshake.
				s.client.ExpectSilence(t, time.Second)
				if n := accepted.Load(); n != 0 {
					t.Errorf("idle server made %d connections to Pub/Sub, want 0", n)
				}
			}

			start := time.Now()
			s.client.CloseInput()
			if code := s.exitCode(t, shutdownBound); code != exitOK {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, exitOK, s.stderr.String())
			}
			t.Logf("run returned %v after the client closed stdin", time.Since(start))

			stderr := s.stderr.String()
			for _, want := range tt.wantLogs {
				if n := strings.Count(stderr, want); n != 1 {
					t.Errorf("stderr has %q %d times, want once; stderr:\n%s", want, n, stderr)
				}
			}
			if !tt.wantReceiving && strings.Contains(stderr, "session initialized; receiving") {
				t.Errorf("idle server logged that it is receiving; stderr:\n%s", stderr)
			}
		})
	}
}

// TestRunLockError checks that an enabled server that cannot take the
// subscription lock exits 1 with one line naming the subscription before the
// handshake, instead of receiving without the lock.
func TestRunLockError(t *testing.T) {
	const subscription = "projects/test-project/subscriptions/lock-error"

	tests := map[string]struct {
		// lockDir returns a lock directory that cannot hold the lock file.
		lockDir func(t *testing.T) string
		// wantCause is the end of the last stderr line.
		wantCause string
	}{
		"error: lock directory cannot be created under a regular file": {
			lockDir: func(t *testing.T) string {
				t.Helper()
				file := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(file, "pubsub-channel")
			},
			wantCause: "not a directory",
		},
		"error: lock file cannot be created in a read-only directory": {
			lockDir: func(t *testing.T) string {
				t.Helper()
				dir := filepath.Join(t.TempDir(), "read-only")
				if err := os.Mkdir(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				// t.TempDir cannot remove the directory's parent otherwise.
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
				return dir
			},
			wantCause: "permission denied",
		},
		"error: lock file is a symbolic link": {
			lockDir: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				const content = "not a lock file\n"
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, lock.Path(dir, subscription)); err != nil {
					t.Fatal(err)
				}
				// Runs after the server has exited.
				t.Cleanup(func() {
					got, err := os.ReadFile(target)
					if err != nil || string(got) != content {
						t.Errorf("link target after the run: %q, %v; want %q unchanged", got, err, content)
					}
				})
				return dir
			},
			wantCause: "is a symbolic link; refusing to use it as the lock file",
		},
		"error: lock file is a hard link to another file": {
			lockDir: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				const content = "not a lock file\n"
				// In dir, so that the link is on the same file system.
				victim := filepath.Join(dir, "victim")
				if err := os.WriteFile(victim, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(victim, lock.Path(dir, subscription)); err != nil {
					t.Fatal(err)
				}
				// Runs after the server has exited.
				t.Cleanup(func() {
					got, err := os.ReadFile(victim)
					if err != nil || string(got) != content {
						t.Errorf("linked file after the run: %q, %v; want %q unchanged", got, err, content)
					}
				})
				return dir
			},
			wantCause: "has 2 hard links; refusing to use it as the lock file",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts, accepted := countingEndpoint(t)
			s := startServer(t, []string{"-enable", "-subscription", subscription}, nil, tt.lockDir(t), opts)

			if code := s.exitCode(t, shutdownBound); code != exitFailure {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, exitFailure, s.stderr.String())
			}
			stderr := strings.TrimRight(s.stderr.String(), "\n")
			if lines := strings.Split(stderr, "\n"); len(lines) != 1 {
				t.Errorf("stderr has %d lines, want 1:\n%s", len(lines), stderr)
			}
			wantPrefix := "pubsub-channel: refusing to receive from " + subscription + " without the subscription lock: "
			if !strings.HasPrefix(stderr, wantPrefix) || !strings.HasSuffix(stderr, tt.wantCause) {
				t.Errorf("stderr = %q, want a line starting with %q and ending with %q", stderr, wantPrefix, tt.wantCause)
			}
			// The server exits before the handshake and without connecting.
			s.client.ExpectSilence(t, 100*time.Millisecond)
			if n := accepted.Load(); n != 0 {
				t.Errorf("server made %d connections to Pub/Sub, want 0", n)
			}
		})
	}
}

// TestRunLockLost checks that a receiving server whose lock file is removed or
// replaced stops receiving and exits 1 within about one lock check interval,
// so that it does not keep receiving beside a process that locked a new file
// at the same path. It also checks that its shutdown leaves the new file's
// holder text alone. It needs no container runtime.
func TestRunLockLost(t *testing.T) {
	const subscription = "projects/test-project/subscriptions/lock-lost"
	const wantLog = `level=ERROR msg="the subscription lock file was removed or replaced; stopped receiving so that no second process receives beside this one"`

	tests := map[string]struct {
		// relock takes the lock again after the removal, as a second process
		// starting then would.
		relock bool
	}{
		"error: lock file removed while receiving": {},
		"error: lock file removed and locked again by another process while receiving": {
			relock: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lockDir := t.TempDir()
			opts, _ := countingEndpoint(t)
			s := startServer(t, []string{"-enable", "-subscription", subscription}, nil, lockDir, opts)
			s.handshake(t)
			s.waitForLog(t, "session initialized; receiving")

			path := lock.Path(lockDir, subscription)
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove the lock file: %v", err)
			}
			var wantHolder string
			if tt.relock {
				l, err := lock.Acquire(lockDir, subscription)
				if err != nil {
					t.Fatalf("lock the new lock file: %v", err)
				}
				t.Cleanup(func() { _ = l.Release() })
				if err := l.WriteHolder(time.Now(), subscription); err != nil {
					t.Fatalf("record the new holder: %v", err)
				}
				wantHolder = lock.Holder(lockDir, subscription)
			}
			removed := time.Now()

			if code := s.exitCode(t, lock.CheckInterval+shutdownBound); code != exitFailure {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, exitFailure, s.stderr.String())
			}
			t.Logf("run returned %v after the lock file was removed", time.Since(removed))
			stderr := s.stderr.String()
			if n := strings.Count(stderr, wantLog); n != 1 {
				t.Errorf("stderr has %q %d times, want once; stderr:\n%s", wantLog, n, stderr)
			}
			if diff := gocmp.Diff(wantHolder, lock.Holder(lockDir, subscription)); diff != "" {
				t.Errorf("holder text of the file now at the lock path (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRunExclusiveAgainstEmulator runs two enabled servers on one subscription
// with one lock directory, as two Claude Code processes on one machine would,
// and checks that only the first receives, and that the second stays idle even
// after the first has exited.
func TestRunExclusiveAgainstEmulator(t *testing.T) {
	tests := map[string]struct {
		first, second *pubsub.Message
	}{
		"success: only the lock holder delivers and the loser never takes over": {
			first:  &pubsub.Message{Data: []byte("for the holder")},
			second: &pubsub.Message{Data: []byte("after the holder exited")},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := testsupport.NewFixture(t, testsupport.EmulatorClient(t))
			lockDir := t.TempDir()
			args := []string{"-enable", "-subscription", f.Subscription}

			holder := startRun(t, args, lockDir)
			// The holder takes the lock before it answers initialize, so the
			// second server starts only once the lock is taken.
			holder.handshake(t)
			holder.waitForLog(t, "session initialized; receiving")

			loser := startRun(t, args, lockDir)
			result := loser.handshake(t)
			if diff := gocmp.Diff(event.InactiveInstructions(f.Subscription), result["instructions"]); diff != "" {
				t.Errorf("loser instructions (-want +got):\n%s", diff)
			}
			loser.waitForLog(t, "another pubsub-channel process on this machine is receiving from "+f.Subscription)

			id := f.Publish(t, tt.first)
			n := testsupport.DecodeNotification(t, holder.client.Next(t, testsupport.DeliveryTimeout), channel.NotificationMethod)
			if diff := gocmp.Diff(id, n.Params.Meta[event.KeyMessageID]); diff != "" {
				t.Errorf("holder delivered another message (-want +got):\n%s", diff)
			}
			loser.client.ExpectSilence(t, 2*time.Second)

			holder.client.CloseInput()
			if code := holder.exitCode(t, 30*time.Second); code != exitOK {
				t.Errorf("holder exit code = %d, want %d; stderr:\n%s", code, exitOK, holder.stderr.String())
			}

			secondID := f.Publish(t, tt.second)
			loser.client.ExpectSilence(t, 3*time.Second)
			if strings.Contains(loser.stderr.String(), "session initialized; receiving") {
				t.Errorf("loser started receiving; stderr:\n%s", loser.stderr.String())
			}
			// Nothing pulled the second message: it is still in the
			// subscription for another subscriber.
			ctx, cancel := context.WithTimeout(t.Context(), testsupport.DeliveryTimeout)
			defer cancel()
			var pulled atomic.Bool
			if err := f.Client.Subscriber(f.Subscription).Receive(ctx, func(_ context.Context, m *pubsub.Message) {
				m.Ack()
				if m.ID == secondID {
					pulled.Store(true)
					cancel()
				}
			}); err != nil {
				t.Fatalf("pull the second message: %v", err)
			}
			if !pulled.Load() {
				t.Errorf("message %s published after the holder exited was not left in the subscription", secondID)
			}

			loser.client.CloseInput()
			if code := loser.exitCode(t, shutdownBound); code != exitOK {
				t.Errorf("loser exit code = %d, want %d; stderr:\n%s", code, exitOK, loser.stderr.String())
			}
		})
	}
}
