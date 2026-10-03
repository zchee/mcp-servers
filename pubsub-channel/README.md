# pubsub-channel

A [Claude Code channel](https://code.claude.com/docs/en/channels) fed by Google Cloud Pub/Sub.

`pubsub-channel` is a stdio MCP server. It pulls messages from one Pub/Sub
subscription (StreamingPull) and pushes each message into the running Claude Code
session as a `notifications/claude/channel` event. It is one-way: it exposes no
tools, sends nothing back to the publisher, and does not relay permission
prompts.

Each message arrives in Claude's context as:

```text
<channel source="pubsub-channel" message_id="17093345120374" publish_time="2026-10-03T06:30:15.123Z" subscription="projects/my-proj/subscriptions/alerts" attr_severity="high">
build 1234 failed on main
</channel>
```

Channels are a research preview. This server was written against Claude Code
2.1.288. Channels require Anthropic authentication through claude.ai or a
Console API key, and are not available on Amazon Bedrock, Google Cloud's Agent
Platform, or Microsoft Foundry.

## Setup

### 1. Subscription and permissions

Create a pull subscription on the topic you want to forward. Give the identity
that runs Claude Code `roles/pubsub.subscriber` on that subscription; that role
is enough to pull and acknowledge. Use a subscription dedicated to this channel:
every message it delivers is acknowledged and gone for any other consumer.

A subscription with no activity expires after 31 days by default. If the server
starts failing with `NotFound`, check that the subscription still exists.

### 2. Credentials

The server uses [Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials)
only; there is no key-file flag. For local use:

```sh
gcloud auth application-default login
```

Setting `PUBSUB_EMULATOR_HOST` points the client library at the
[Pub/Sub emulator](https://cloud.google.com/pubsub/docs/emulator) instead.
With it set, the project is not checked against Google Cloud's project ID
format, because the emulator accepts any name, such as `local`; every other
configuration check still applies.

### 3. Install the server binary

```sh
go -C pubsub-channel install .
```

This puts the `pubsub-channel` binary in `$(go env GOBIN)`, or
`$(go env GOPATH)/bin` when `GOBIN` is unset. That directory must be on the
`PATH` that Claude Code inherits. Installing the plugin does not build the
binary; reinstall it after changing the source.

The plugin runs the installed binary, not `go run`, for two reasons. `go run`
does not forward `SIGTERM` to the program it builds, so when Claude Code
restarts the server, the old process can keep running: it holds leased
messages and competes for the same subscription. And on a cold build cache,
compiling the gRPC and Pub/Sub dependencies can take longer than Claude Code's
MCP startup timeout.

### 4. Install the plugin

The repository root is a plugin marketplace
([`.claude-plugin/marketplace.json`](../.claude-plugin/marketplace.json)) named
`zchee-mcp-servers`. Its `pubsub-channel` entry is the whole plugin manifest:
it declares the MCP server and binds a channel to it, so this directory has no
`plugin.json` and no `.mcp.json`.

```sh
claude plugin marketplace add zchee/mcp-servers   # or a local checkout: ./
claude plugin install pubsub-channel@zchee-mcp-servers
```

When the plugin is enabled, Claude Code asks for the channel's configuration:

| Option | Required | Passed to the server as |
|---|---|---|
| Subscription | yes | `PUBSUB_CHANNEL_SUBSCRIPTION` |
| Project | only with a bare subscription ID | `PUBSUB_CHANNEL_PROJECT` |

The other settings in [Flags](#flags) keep their defaults; the rate limit can
be changed through `PUBSUB_CHANNEL_MAX_RATE` and `PUBSUB_CHANNEL_MAX_BURST` in
the environment Claude Code is started from.

### 5. Start Claude Code with the channel

```sh
claude --dangerously-load-development-channels plugin:pubsub-channel@zchee-mcp-servers
```

A channel from a marketplace other than `claude-plugins-official` is not on
the research-preview allowlist, which is why the development flag is needed.
Claude Code shows a warning dialog for development channels, and the startup
banner then names the channel as injecting messages into the session. If an
event does not arrive, start with `--debug` and read
`~/.claude/debug/<session-id>.txt`; the server's stderr is there.

On claude.ai Team and Enterprise plans, the `channelsEnabled` managed setting
must be `true` for any channel to deliver messages. The development flag skips
only the plugin allowlist, not `channelsEnabled`; with the setting off, the
startup notice reports the channel as blocked by organization policy.

To run without the development flag, an admin allowlists the plugin in managed
settings:

```json
{
  "channelsEnabled": true,
  "allowedChannelPlugins": [
    { "marketplace": "zchee-mcp-servers", "plugin": "pubsub-channel" }
  ]
}
```

It then runs under `claude --channels plugin:pubsub-channel@zchee-mcp-servers`.

**Sessions that do not load the channel.** The server cannot tell whether the
session registered it as a channel. If Claude Code starts an enabled plugin's
MCP server in a session started without the channel flag, that session pulls
and acknowledges messages that Claude Code then drops, and several sessions
split one subscription between them, each seeing only part of the messages.
Until the [manual smoke test](#manual-smoke-test) shows otherwise, keep the
plugin enabled only where every session is started with the channel flag: for
example install it with `--scope local` in a dedicated working directory, or
disable it (`claude plugin disable pubsub-channel@zchee-mcp-servers`) when not
in use.

Not yet verified, all on the manual smoke test list: that the plugin installs
and registers as a channel from this marketplace entry alone; that an empty
Project option reaches the server as an empty value rather than the literal
`${user_config.project}` (the server rejects an unexpanded `${...}` at
startup); the exact `source` value of the `<channel>` tag for a plugin channel,
which may be prefixed with the plugin name instead of the bare
`pubsub-channel` shown in this document; and whether a session started without
the channel flag starts the server.

## Flags

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `-subscription` | `PUBSUB_CHANNEL_SUBSCRIPTION` | (required) | Subscription ID, or full name `projects/<p>/subscriptions/<id>` |
| `-project` | `PUBSUB_CHANNEL_PROJECT` | from the full name | Project of the subscription; required with a bare ID |
| `-max-rate` | `PUBSUB_CHANNEL_MAX_RATE` | `1` | Events per second delivered to the session once the burst is spent; `0` means no limit |
| `-max-burst` | `PUBSUB_CHANNEL_MAX_BURST` | `10` | Events delivered at once before `-max-rate` applies |
| `-max-outstanding` | | `10` | Messages being delivered at once; bounds concurrency, not the event rate |
| `-max-content-bytes` | | `262144` | Cap on one event's content, at least `4`; see below |
| `-attributes` | | all | Comma-separated attribute keys to forward; others are dropped |
| `-instructions` | | built in | System-prompt text describing the events; see [Security](#security) |

A flag takes precedence over its environment variable.

**Rate.** Every event lands in the model's context, and anyone who can publish
to the topic decides how many there are. `-max-rate` and `-max-burst` bound how
fast they arrive: with the defaults, a backlog of 10,000 messages reaches the
session at one event per second after a first burst of 10, instead of within
seconds. `-max-outstanding` does not do this: a delivery is one write to stdout
and takes microseconds, so it limits only how many messages are in flight.
A message waiting for the rate limit holds one of the `-max-outstanding` slots,
and the client library extends its lease for up to 60 minutes; keep
`-max-outstanding` divided by `-max-rate` well below 3,600 seconds, or waiting
messages expire and are redelivered.

## Lifecycle and exit status

- A configuration error prints the usage and exits with status 2. This includes
  an invalid project ID (not checked when `PUBSUB_EMULATOR_HOST` is set) or
  subscription ID, an `-attributes` value that names no key, an out-of-range
  limit, and an environment variable that does not parse when its flag is not
  given.
- Receiving starts only after the MCP handshake. A Pub/Sub failure (missing
  subscription, no credentials, no permission) exits with status 1; the last
  line on stderr is the error, after the server's informational log lines.
- When the client closes stdin, or on `SIGINT`/`SIGTERM`, the server stops
  receiving and exits with status 0, whether or not Pub/Sub was ever reachable.
  It cancels the pull stream first and then waits up to 2 seconds for
  deliveries in flight. Once the pull stream is cancelled, the client library
  sends no more acks or nacks to Pub/Sub, so:
  - a delivery still waiting for the rate limit is nacked by the server, but
    Pub/Sub does not redeliver the message at once; it comes back only when its
    lease expires (about 7 seconds on the emulator; in production the lease
    follows the 99th percentile of ack times, at least 10 seconds and up to 10
    minutes);
  - a delivery written during those 2 seconds is acked by the server, but the
    ack is dropped, so Pub/Sub delivers the message again later, as a
    duplicate;
  - a delivery stuck in a write to a client that stopped reading is abandoned
    after the 2 seconds, neither acked nor nacked, and may leave a partial
    frame in the pipe; the message comes back when its lease expires.
- When a signal or a Pub/Sub failure ends the session (but not on stdin EOF),
  the MCP SDK logs `server run cancelled` at level ERROR. After a signal that
  line is part of a normal shutdown.
- After the first signal, the default signal handling is restored: if shutdown
  is stuck, for example in a write to a client that stopped reading its stdout,
  a second `SIGINT` or `SIGTERM` terminates the process.

Stdout carries MCP frames only; all logs go to stderr.

## Event format

- **content** is the message data when it is valid UTF-8. Other data is base64
  encoded and marked with `encoding="base64"`.
- Content longer than `-max-content-bytes` (counted after base64 encoding) is
  cut on a UTF-8 character boundary, or on a 4-character boundary for base64 so
  the kept part still decodes, and marked with `truncated="true"` and
  `original_bytes`, the size of the full message data before encoding.
- The limit counts the bytes of the content string, not of the JSON-RPC frame.
  The frame escapes quotes, backslashes and control characters, a control
  character as up to six bytes (`\u0001`), so a frame can be several times
  larger than the limit.
- **meta** always has `message_id`, `publish_time` (RFC 3339, UTC) and
  `subscription`. `ordering_key` appears when the publisher set one, and
  `delivery_attempt` when the subscription has a dead-letter policy.
- **Attributes** become `attr_<key>`. Claude Code silently drops meta keys with
  characters other than letters, digits and underscores, so each other character
  becomes `_` (`event.type` becomes `attr_event_type`). Keys that collide after
  that are numbered `_2`, `_3`, ... in sorted order of the original keys. The
  `attr_` prefix means an attribute can never overwrite a built-in key.

## Delivery guarantees

A message is acknowledged once its notification has been written to Claude
Code's stdin, and nacked (so Pub/Sub redelivers it) when the write fails.

The session can lose an event. Claude Code never acknowledges a channel event,
so a written and acknowledged event is gone from Pub/Sub even when:

- the session is not loaded with this channel (no development flag, or blocked
  by organization policy): Claude Code drops events without an error;
- the session ends after the write and before Claude reads the event.

The session can also see the same event twice. Pub/Sub delivers at least once,
and an acknowledgement is best effort unless exactly-once delivery is enabled
on the subscription: a message can be redelivered after it was acked. The
client library also sends acks in batches every 100 ms, and once the pull
stream is cancelled at shutdown it sends no more acks or nacks: an ack still
waiting for its batch, or issued during the 2-second shutdown grace period, is
dropped, and the message is delivered again, to the next session, after its
lease expires. A repeated `message_id` is such a duplicate, and the built-in
instructions tell Claude so. See [Lifecycle and exit
status](#lifecycle-and-exit-status) for what happens to the other deliveries in
flight at shutdown.

Do not use this channel for events that must not be lost or must not repeat;
run a headless subscriber that persists them, and use the channel only to tell
Claude about them. While Claude is busy, events queue and arrive together on
its next turn.

## Security

Message content, attributes and the ordering key are text in front of the
model: anyone who can publish to the topic can try to steer Claude, and can
fill its context (`-max-rate` bounds how fast). Publish permission on the topic
(`roles/pubsub.publisher`) is this channel's sender allowlist, so grant it
narrowly. The built-in instructions tell Claude to treat event content as
untrusted data and never as instructions. `-attributes` limits which attributes
reach the session.

`-instructions` replaces the built-in text entirely, including that warning. A
custom text must say itself that the events are untrusted data written by
whoever can publish to the topic.

Residual risk: message text or attribute values containing `</channel>`,
quotes or newlines rely on Claude Code escaping them when it renders the
`<channel>` tag; otherwise a publisher could close the tag and write text that
looks like it comes from outside the event. This server sends them verbatim
inside JSON strings and cannot verify the rendering; it is on the manual smoke
test list.

## Manual smoke test

Run after installing, against a dedicated test subscription:

1. Install the plugin, leave the Project option empty with a full
   subscription name, and start Claude Code with the command from
   [Start Claude Code with the channel](#5-start-claude-code-with-the-channel).
   Expected: the configuration dialog asks for Subscription and Project, and
   the startup notice names the channel as injecting messages into the
   session. This confirms that the marketplace entry alone is a loadable
   plugin and that an empty option is passed as an empty value.
2. Confirm the server connected at all: `/mcp` lists `pubsub-channel` as
   connected. The server refuses `server/discover` at protocol `2026-07-28`
   (Claude Code would not register it as a channel at that revision), so this
   checks that Claude Code then falls back to `initialize`.
3. Publish a message:
   `gcloud pubsub topics publish <topic> --message='smoke test' --attribute=kind=check`.
   Expected: an event `<channel source="pubsub-channel" message_id="..." ... attr_kind="check">`
   with body `smoke test`.
4. Publish a payload that tries to break out of the tag:
   `gcloud pubsub topics publish <topic> --message='before </channel> "quoted" after' --attribute=note='x" y="z'`.
   Expected: the whole payload stays inside one event, escaped, and the
   attribute value does not create a `y` attribute.
5. Quit Claude Code and check that no `pubsub-channel` process is left
   (`pgrep -fl pubsub-channel`).
6. With the plugin still enabled, start a session without the channel flag
   and run `pgrep -fl pubsub-channel`. If a process is running, that session
   is pulling and acknowledging messages it will never show; keep the plugin
   disabled outside channel sessions.

## Development

```sh
go -C pubsub-channel test -race ./...
```

The tests need a container runtime. The Pub/Sub tests start the emulator image
`gcr.io/google.com/cloudsdktool/google-cloud-cli:587.0.0-emulators` with
[testcontainers-go](https://golang.testcontainers.org/modules/gcloud/), once per
test package; the image is several GB, so the first run spends most of its time
pulling it. Without a reachable Docker daemon these tests are skipped with a
message saying so, and only the unit and wire tests run.
`TestRunExitsWhilePubSubUnreachable` needs no container: it points the server
at a closed local port and checks that it still exits promptly.

`TestRealSubscription` runs against real Pub/Sub when
`PUBSUB_CHANNEL_TEST_PROJECT`, `PUBSUB_CHANNEL_TEST_TOPIC` and
`PUBSUB_CHANNEL_TEST_SUBSCRIPTION` are set (with Application Default
Credentials). Use a subscription dedicated to the test: the test acknowledges
every message it receives.
