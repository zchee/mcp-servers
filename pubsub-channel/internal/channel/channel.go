// Package channel serves a one-way Claude Code channel over MCP and pushes
// events into the connected session as channel notifications.
//
// Claude Code registers an MCP server as a channel when the server declares the
// experimental "claude/channel" capability. Events then arrive in the session as
// "notifications/claude/channel" JSON-RPC notifications. The Go MCP SDK has no
// public API for sending a notification with a custom method from the server,
// so Channel captures the transport's connection and writes the notification
// itself.
package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/go-json-experiment/json"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// NotificationMethod is the JSON-RPC method Claude Code routes to its
	// channel handler.
	NotificationMethod = "notifications/claude/channel"

	// CapabilityKey is the experimental server capability that makes Claude Code
	// register the server as a channel.
	CapabilityKey = "claude/channel"

	// firstUnregisteredVersion is the oldest MCP protocol revision at which
	// Claude Code no longer registers a server as a channel. Every revision at or
	// after it is withheld from negotiation.
	firstUnregisteredVersion = "2026-07-28"
)

var (
	// ErrNotInitialized is returned by [Channel.Notify] before the client has
	// sent "notifications/initialized". Claude Code drops events it receives
	// before the handshake completes, so they are refused rather than lost.
	ErrNotInitialized = errors.New("channel: client has not completed initialization")

	// ErrClosed is returned by [Channel.Notify] after the session has ended.
	ErrClosed = errors.New("channel: session closed")
)

// Options configures a [Channel].
type Options struct {
	// Name and Version identify the server in the initialize response.
	Name    string
	Version string

	// Instructions are added to the client's system prompt. They should tell the
	// model what the events are and how far to trust them.
	Instructions string

	// Logger receives the SDK's diagnostics. It must not write to stdout, which
	// carries protocol frames only.
	Logger *slog.Logger
}

// Channel is an MCP server that serves one session and can push channel
// notifications into it.
type Channel struct {
	server *mcp.Server

	initialized chan struct{}
	initOnce    sync.Once

	mu     sync.Mutex
	conn   mcp.Connection
	closed bool
}

// New returns a Channel configured with opts.
func New(opts Options) *Channel {
	c := &Channel{initialized: make(chan struct{})}
	c.server = mcp.NewServer(&mcp.Implementation{Name: opts.Name, Version: opts.Version}, &mcp.ServerOptions{
		Instructions: opts.Instructions,
		Logger:       opts.Logger,
		InitializedHandler: func(context.Context, *mcp.InitializedRequest) {
			c.initOnce.Do(func() { close(c.initialized) })
		},
		// A non-nil Capabilities replaces the SDK's default {"logging":{}}. The
		// inner value must be an empty object, not null, for Claude Code to
		// recognise the capability.
		Capabilities: &mcp.ServerCapabilities{
			Experimental: map[string]any{CapabilityKey: map[string]any{}},
		},
		SupportedProtocolVersions: ProtocolVersions(),
	})
	return c
}

// ProtocolVersions returns the MCP protocol revisions the channel negotiates:
// every revision the SDK supports that Claude Code still registers as a channel.
func ProtocolVersions() []string {
	return slices.DeleteFunc(mcp.SupportedProtocolVersions(), func(v string) bool {
		return v >= firstUnregisteredVersion
	})
}

// Run serves one MCP session over t until the client disconnects or ctx is
// cancelled. A Channel must be run at most once.
func (c *Channel) Run(ctx context.Context, t mcp.Transport) error {
	defer func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
	}()
	return c.server.Run(ctx, &capturingTransport{Transport: t, channel: c})
}

// Initialized returns a channel that is closed once the client has sent
// "notifications/initialized".
func (c *Channel) Initialized() <-chan struct{} {
	return c.initialized
}

// notificationParams is the params object of a channel notification. Claude
// Code renders content as the event body and each meta entry as an attribute of
// the event tag; it drops meta keys that are not letters, digits and
// underscores.
type notificationParams struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitzero"`
}

// Notify sends one channel notification carrying content and meta.
//
// A nil error means the notification was written to the transport. Claude Code
// never acknowledges a channel notification, so a successful write is the only
// delivery signal there is.
func (c *Channel) Notify(ctx context.Context, content string, meta map[string]string) error {
	select {
	case <-c.initialized:
	default:
		return ErrNotInitialized
	}

	c.mu.Lock()
	conn, closed := c.conn, c.closed
	c.mu.Unlock()
	if closed || conn == nil {
		return ErrClosed
	}

	// Deterministic output sorts the meta keys, so identical events produce
	// identical bytes.
	params, err := json.Marshal(notificationParams{Content: content, Meta: meta}, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("channel: encode params: %w", err)
	}
	// A request without an ID is a notification; its encoding omits "id".
	if err := conn.Write(ctx, &jsonrpc.Request{Method: NotificationMethod, Params: params}); err != nil {
		return fmt.Errorf("channel: write notification: %w", err)
	}
	return nil
}

// capturingTransport records the connection its wrapped transport returns.
//
// The connection is handed to the SDK unchanged: the SDK reports session state
// to it through an unexported interface, which a wrapper type could not
// implement.
type capturingTransport struct {
	mcp.Transport
	channel *Channel
}

// Connect implements [mcp.Transport].
func (t *capturingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.channel.mu.Lock()
	t.channel.conn = conn
	t.channel.mu.Unlock()
	return conn, nil
}
