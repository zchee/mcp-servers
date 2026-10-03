// Command pubsub-channel is a Claude Code channel fed by Google Cloud Pub/Sub.
//
// It is a stdio MCP server that pulls messages from one Pub/Sub subscription
// and pushes each one into the running Claude Code session as a channel event.
// The channel is one-way: it exposes no tools and sends nothing back.
//
// Usage:
//
//	pubsub-channel -subscription projects/my-project/subscriptions/my-sub
//
// Stdout carries MCP protocol frames only; every log line goes to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	pubsub "cloud.google.com/go/pubsub/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/option"

	"github.com/zchee/mcp-servers/pubsub-channel/internal/bridge"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/channel"
	"github.com/zchee/mcp-servers/pubsub-channel/internal/event"
)

const serverName = "pubsub-channel"

// Environment variables read when the matching flag is not given.
const (
	envProject      = "PUBSUB_CHANNEL_PROJECT"
	envSubscription = "PUBSUB_CHANNEL_SUBSCRIPTION"
	envMaxRate      = "PUBSUB_CHANNEL_MAX_RATE"
	envMaxBurst     = "PUBSUB_CHANNEL_MAX_BURST"
)

// envEmulatorHost is the client library's variable that selects the Pub/Sub
// emulator. The emulator accepts project names that Google Cloud does not, so
// the project ID format is not checked when it is set.
const envEmulatorHost = "PUBSUB_EMULATOR_HOST"

// Process exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The first signal starts a graceful shutdown. Restoring the default
	// handling right after lets a second signal terminate the process when that
	// shutdown is stuck, for example in a write to a client that stopped
	// reading.
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := run(ctx, os.Args[1:], os.Getenv, &mcp.StdioTransport{}, os.Stderr)
	stop()
	os.Exit(code)
}

// config is the validated command-line configuration.
type config struct {
	project         string
	subscription    string // full resource name: projects/<project>/subscriptions/<id>
	maxOutstanding  int
	maxRate         float64 // events per second; 0 means no limit
	maxBurst        int
	maxContentBytes int
	attributes      []string // nil forwards every attribute
	instructions    string
}

var (
	// subscriptionIDPattern is Pub/Sub's rule for a subscription ID: a letter,
	// then 2 to 254 letters, digits or -_.~+%.
	subscriptionIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9\-_.~+%]{2,254}$`)

	// subscriptionNamePattern matches a full subscription resource name.
	subscriptionNamePattern = regexp.MustCompile(`^projects/([^/]+)/subscriptions/([^/]+)$`)

	// projectIDPattern is Google Cloud's rule for a project ID: 6 to 30
	// lowercase letters, digits or hyphens, starting with a letter and not
	// ending with a hyphen. Legacy domain-scoped projects prefix it with
	// "domain.tld:". A numeric project number is also accepted, since Google
	// Cloud resource names may use either.
	projectIDPattern = regexp.MustCompile(`^(?:(?:[a-z0-9-]+(?:\.[a-z0-9-]+)+:)?[a-z][a-z0-9-]{4,28}[a-z0-9]|[0-9]+)$`)
)

// parseConfig parses args, falling back to getenv for the project, the
// subscription and the rate limit. Flag errors and the usage text are written
// to stderr.
func parseConfig(args []string, getenv func(string) string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet(serverName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		// A failed write to stderr has nowhere to be reported.
		_, _ = fmt.Fprintf(fs.Output(), "Usage: %s -subscription <id or projects/<p>/subscriptions/<id>> [flags]\n\n", serverName)
		_, _ = fmt.Fprintf(fs.Output(), "A Claude Code channel that delivers messages from one Google Cloud Pub/Sub subscription.\n")
		_, _ = fmt.Fprintf(fs.Output(), "Credentials come from Application Default Credentials; PUBSUB_EMULATOR_HOST selects the emulator.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	var cfg config
	var attributes string
	fs.StringVar(&cfg.project, "project", getenv(envProject), "Google Cloud project of the subscription; optional when -subscription is a full resource name (env "+envProject+")")
	fs.StringVar(&cfg.subscription, "subscription", getenv(envSubscription), "subscription ID, or full name projects/<p>/subscriptions/<id> (env "+envSubscription+")")
	fs.IntVar(&cfg.maxOutstanding, "max-outstanding", bridge.DefaultMaxOutstandingMessages, "maximum number of messages being delivered at once; bounds concurrency, not the event rate")
	fs.Float64Var(&cfg.maxRate, "max-rate", bridge.DefaultMaxRate, "maximum events per second delivered to the session once the burst is spent; 0 means no limit (env "+envMaxRate+")")
	fs.IntVar(&cfg.maxBurst, "max-burst", bridge.DefaultMaxBurst, "maximum events delivered at once before -max-rate applies (env "+envMaxBurst+")")
	fs.IntVar(&cfg.maxContentBytes, "max-content-bytes", event.DefaultMaxContentBytes, "maximum size in bytes of one event's content, at least "+strconv.Itoa(event.MinContentBytes)+"; longer content is truncated")
	fs.StringVar(&attributes, "attributes", "", "comma-separated message attribute keys to forward; empty forwards all")
	fs.StringVar(&cfg.instructions, "instructions", "", "system-prompt instructions for the channel; empty uses a built-in description of the events. A custom text replaces the built-in warning that events are untrusted, so it must give one itself")

	// The flag package prints the usage itself for a parse error.
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	// The rate-limit variables are read only for a flag not given on the
	// command line, so a flag overrides even a malformed value.
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if v := getenv(envMaxRate); v != "" && !set["max-rate"] {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			fs.Usage()
			return config{}, fmt.Errorf("%s=%q is not a number", envMaxRate, v)
		}
		cfg.maxRate = f
	}
	if v := getenv(envMaxBurst); v != "" && !set["max-burst"] {
		n, err := strconv.Atoi(v)
		if err != nil {
			fs.Usage()
			return config{}, fmt.Errorf("%s=%q is not an integer", envMaxBurst, v)
		}
		cfg.maxBurst = n
	}
	cfg, err := validate(cfg, attributes, fs.Args(), getenv(envEmulatorHost) != "")
	if err != nil {
		fs.Usage()
		return config{}, err
	}
	return cfg, nil
}

// validate checks cfg, normalises the subscription to its full resource name,
// and fills in what is derived from the other settings. attributes is the raw
// -attributes value, rest the positional arguments, and emulator reports
// whether the Pub/Sub emulator is selected.
func validate(cfg config, attributes string, rest []string, emulator bool) (config, error) {
	if len(rest) > 0 {
		return config{}, fmt.Errorf("unexpected arguments: %q", rest)
	}

	for _, v := range []struct{ name, value string }{{"project", cfg.project}, {"subscription", cfg.subscription}} {
		if strings.Contains(v.value, "${") {
			return config{}, fmt.Errorf("%s %q looks like an unexpanded ${VAR} reference; set the variable in the environment Claude Code runs in", v.name, v.value)
		}
	}

	switch {
	case cfg.subscription == "":
		return config{}, fmt.Errorf("a subscription is required: set -subscription or %s", envSubscription)
	case strings.Contains(cfg.subscription, "/"):
		match := subscriptionNamePattern.FindStringSubmatch(cfg.subscription)
		if match == nil {
			return config{}, fmt.Errorf("subscription %q is neither an ID nor a name of the form projects/<project>/subscriptions/<id>", cfg.subscription)
		}
		project, id := match[1], match[2]
		if cfg.project != "" && cfg.project != project {
			return config{}, fmt.Errorf("project %q conflicts with the project %q in subscription %q", cfg.project, project, cfg.subscription)
		}
		if err := validateSubscriptionID(id); err != nil {
			return config{}, err
		}
		cfg.project = project
	default:
		if err := validateSubscriptionID(cfg.subscription); err != nil {
			return config{}, err
		}
		if cfg.project == "" {
			return config{}, fmt.Errorf("a project is required for subscription ID %q: set -project or %s, or pass the full subscription name", cfg.subscription, envProject)
		}
		cfg.subscription = "projects/" + cfg.project + "/subscriptions/" + cfg.subscription
	}
	// Checked here rather than left to Pub/Sub, which would reject it only
	// after the MCP handshake, as a runtime failure instead of a usage error.
	// The emulator accepts any project name, such as "local".
	if !emulator && !projectIDPattern.MatchString(cfg.project) {
		return config{}, fmt.Errorf("project %q is not a valid Google Cloud project ID", cfg.project)
	}

	if cfg.maxOutstanding < 1 {
		return config{}, fmt.Errorf("-max-outstanding must be at least 1, got %d", cfg.maxOutstanding)
	}
	// flag.Float64Var accepts "NaN" and "Inf"; zero is the only way to
	// disable the limit.
	if cfg.maxRate < 0 || math.IsNaN(cfg.maxRate) || math.IsInf(cfg.maxRate, 0) {
		return config{}, fmt.Errorf("-max-rate must be a finite number of events per second, 0 or more, got %v", cfg.maxRate)
	}
	if cfg.maxBurst < 1 {
		return config{}, fmt.Errorf("-max-burst must be at least 1, got %d", cfg.maxBurst)
	}
	// A smaller cap keeps no data at all in some encodings, so every event
	// would arrive empty.
	if cfg.maxContentBytes < event.MinContentBytes {
		return config{}, fmt.Errorf("-max-content-bytes must be at least %d, got %d", event.MinContentBytes, cfg.maxContentBytes)
	}
	if attributes != "" {
		cfg.attributes = []string{}
		for key := range strings.SplitSeq(attributes, ",") {
			if key = strings.TrimSpace(key); key != "" {
				cfg.attributes = append(cfg.attributes, key)
			}
		}
		// An empty allowlist would silently forward nothing, the opposite of
		// leaving the flag unset.
		if len(cfg.attributes) == 0 {
			return config{}, fmt.Errorf("-attributes %q names no attribute key; omit the flag to forward every attribute", attributes)
		}
	}
	if cfg.instructions == "" {
		cfg.instructions = event.Instructions(cfg.subscription)
	}
	return cfg, nil
}

// validateSubscriptionID reports whether id is a valid Pub/Sub subscription ID.
func validateSubscriptionID(id string) error {
	// Pub/Sub reserves IDs starting with "goog".
	if !subscriptionIDPattern.MatchString(id) || strings.HasPrefix(id, "goog") {
		return fmt.Errorf("subscription ID %q is not a valid Pub/Sub subscription ID", id)
	}
	return nil
}

// run serves the channel over transport until the client disconnects, ctx is
// cancelled, or Pub/Sub fails, and returns the process exit code. clientOpts
// are passed to the Pub/Sub client.
func run(ctx context.Context, args []string, getenv func(string) string, transport mcp.Transport, stderr io.Writer, clientOpts ...option.ClientOption) int {
	cfg, err := parseConfig(args, getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", serverName, err)
		return exitUsage
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil)).With("subscription", cfg.subscription)

	client, err := pubsub.NewClient(ctx, cfg.project, clientOpts...)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: connect to Pub/Sub project %s: %v\n", serverName, cfg.project, err)
		return exitFailure
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Warn("close Pub/Sub client", "error", err)
		}
	}()

	sub := client.Subscriber(cfg.subscription)
	sub.ReceiveSettings.MaxOutstandingMessages = cfg.maxOutstanding

	ch := channel.New(channel.Options{Name: serverName, Version: moduleVersion(), Instructions: cfg.instructions, Logger: logger})
	b := bridge.New(ch, event.Options{Subscription: cfg.subscription, MaxContentBytes: cfg.maxContentBytes, Attributes: cfg.attributes}, bridge.NewLimiter(cfg.maxRate, cfg.maxBurst), logger)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sessionDone := make(chan error, 1)
	go func() {
		sessionDone <- ch.Run(ctx, transport)
		// The session is the reason the process exists; once it ends, stop
		// receiving so no message is pulled that could not be delivered.
		cancel()
	}()

	// Receiving starts only after the handshake: Claude Code drops events sent
	// before it, and a message pulled earlier would be nacked for nothing.
	select {
	case <-ch.Initialized():
	case err := <-sessionDone:
		return sessionExitCode(err, stderr)
	}
	logger.Info("session initialized; receiving")

	recvErr := b.Run(ctx, sub)
	cancel()
	sessionErr := <-sessionDone
	if recvErr != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", serverName, recvErr)
		return exitFailure
	}
	return sessionExitCode(sessionErr, stderr)
}

// sessionExitCode maps the error that ended the MCP session to an exit code.
// The client closing stdin and a signal are normal shutdowns.
func sessionExitCode(err error, stderr io.Writer) int {
	if err == nil || errors.Is(err, context.Canceled) {
		return exitOK
	}
	_, _ = fmt.Fprintf(stderr, "%s: MCP session: %v\n", serverName, err)
	return exitFailure
}

// moduleVersion reports the main module version recorded in the binary.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
