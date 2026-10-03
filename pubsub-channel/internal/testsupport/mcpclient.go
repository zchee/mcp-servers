// Package testsupport holds helpers shared by this module's tests: a scripted
// MCP client that talks to a server over pipes as raw JSON lines, and a Pub/Sub
// emulator run in a container.
//
// The scripted client exists because the Go MCP SDK client cannot register a
// handler for a custom notification method, and because the tests assert the
// exact bytes the server writes.
package testsupport

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LineTimeout bounds a wait for a server line that involves no Pub/Sub round
// trip.
const LineTimeout = 5 * time.Second

// Client plays the client side of an MCP session over pipes.
type Client struct {
	in    *io.PipeWriter // client -> server
	out   *io.PipeReader // server -> client
	lines chan []byte

	// problems collects what the reader goroutine finds; it is reported from
	// the test cleanup, because a goroutine must not report after its test ends.
	mu       sync.Mutex
	problems []error
}

// NewClient returns a Client and the transport a server under test must serve.
// Every line the server writes must decode as a JSON-RPC message, or the test
// fails: stdout is reserved for protocol frames.
func NewClient(t testing.TB) (*Client, mcp.Transport) {
	t.Helper()
	serverIn, clientIn := io.Pipe()
	clientOut, serverOut := io.Pipe()
	c := &Client{in: clientIn, out: clientOut, lines: make(chan []byte, 64)}
	readerDone, stop := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(c.lines)
		sc := bufio.NewScanner(clientOut)
		sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
		for sc.Scan() {
			line := slices.Clone(sc.Bytes())
			if _, err := jsonrpc.DecodeMessage(line); err != nil {
				c.addProblem(fmt.Errorf("server wrote a line that is not a JSON-RPC message: %q: %w", line, err))
			}
			select {
			case c.lines <- line:
			case <-stop: // the test is over and nobody reads lines any more
				return
			}
		}
		// A closed pipe is how every session here ends; anything else, such as
		// an over-long line, is a defect.
		if err := sc.Err(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			c.addProblem(fmt.Errorf("read server output: %w", err))
		}
	}()
	t.Cleanup(func() {
		// Closing an io.Pipe end always returns nil.
		_ = clientIn.Close()
		_ = clientOut.Close()
		close(stop)
		<-readerDone
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, err := range c.problems {
			t.Error(err)
		}
	})
	return c, &mcp.IOTransport{Reader: serverIn, Writer: serverOut}
}

func (c *Client) addProblem(err error) {
	c.mu.Lock()
	c.problems = append(c.problems, err)
	c.mu.Unlock()
}

// Send writes one raw JSON-RPC line to the server.
func (c *Client) Send(t testing.TB, line string) {
	t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		t.Fatalf("client write %q: %v", line, err)
	}
}

// Next returns the next line the server wrote, failing after timeout.
func (c *Client) Next(t testing.TB, timeout time.Duration) []byte {
	t.Helper()
	select {
	case line, ok := <-c.lines:
		if !ok {
			t.Fatal("server output closed while a line was expected")
		}
		return line
	case <-time.After(timeout):
		t.Fatalf("no line from the server within %v", timeout)
		return nil
	}
}

// ExpectSilence fails if the server writes a line within d.
func (c *Client) ExpectSilence(t testing.TB, d time.Duration) {
	t.Helper()
	select {
	case line, ok := <-c.lines:
		if ok {
			t.Errorf("unexpected line from the server within %v: %s", d, line)
		}
	case <-time.After(d):
	}
}

// Initialize sends an initialize request asking for version and returns the
// raw response line.
func (c *Client) Initialize(t testing.TB, version string) []byte {
	t.Helper()
	c.Send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"scripted-client","version":"0.0.0"}}}`)
	return c.Next(t, LineTimeout)
}

// SendInitialized sends notifications/initialized. The server processes it
// asynchronously; callers that depend on it must wait for the server to
// signal it.
func (c *Client) SendInitialized(t testing.TB) {
	t.Helper()
	c.Send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

// ExpectPing sends a ping and fails unless the very next server line is its
// response, which proves the server wrote nothing else in between.
func (c *Client) ExpectPing(t testing.TB, id int) {
	t.Helper()
	c.Send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(id)+`,"method":"ping"}`)
	got, want := string(c.Next(t, LineTimeout)), `{"jsonrpc":"2.0","id":`+strconv.Itoa(id)+`,"result":{}}`
	if got != want {
		t.Fatalf("line after ping is not the ping response:\nwant %s\n got %s", want, got)
	}
}

// CloseInput closes the server's input, as a client exiting does.
func (c *Client) CloseInput() { _ = c.in.Close() }

// CloseOutput stops reading the server's output; the server's next write fails.
func (c *Client) CloseOutput() { _ = c.out.Close() }

// Notification is the decoded form of a channel notification line.
type Notification struct {
	Method string `json:"method"`
	Params struct {
		Content string            `json:"content"`
		Meta    map[string]string `json:"meta"`
	} `json:"params"`
}

// DecodeNotification decodes line as a notification with method.
func DecodeNotification(t testing.TB, line []byte, method string) Notification {
	t.Helper()
	var n Notification
	if err := json.Unmarshal(line, &n); err != nil {
		t.Fatalf("decode notification %q: %v", line, err)
	}
	if n.Method != method {
		t.Fatalf("line is not a %s notification: %s", method, line)
	}
	return n
}
