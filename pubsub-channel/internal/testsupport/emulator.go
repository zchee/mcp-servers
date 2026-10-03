package testsupport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpubsub "github.com/testcontainers/testcontainers-go/modules/gcloud/pubsub"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// EmulatorImage is pinned so the emulator under test does not change
	// between runs.
	EmulatorImage = "gcr.io/google.com/cloudsdktool/google-cloud-cli:587.0.0-emulators"

	// AckDeadline is the ack deadline of every fixture subscription; an
	// unacked message is redelivered once it passes.
	AckDeadline = 10 * time.Second

	// DeliveryTimeout bounds the wait for a published message to come out of a
	// subscription.
	DeliveryTimeout = 30 * time.Second

	// emulatorStartTimeout bounds starting the shared container, including
	// pulling the image, which is several GB on the first run.
	emulatorStartTimeout = 10 * time.Minute
)

// emulator is the container shared by every test of one test binary. A
// test-scoped cleanup would stop it under the tests that run after the one that
// started it, so TestMain terminates it through TerminateEmulator instead.
var emulator struct {
	once sync.Once
	ctr  *tcpubsub.Container
	err  error
}

// EmulatorProject returns the emulator's project ID and the client options
// that connect to it, starting the container on first use. It skips the test
// when no container runtime is reachable.
//
// The options select the emulator explicitly instead of through
// PUBSUB_EMULATOR_HOST, so parallel tests never mutate the process environment.
func EmulatorProject(t testing.TB) (string, []option.ClientOption) {
	t.Helper()
	tt, ok := t.(*testing.T)
	if !ok {
		t.Fatalf("EmulatorProject needs a *testing.T, got %T", t)
	}
	testcontainers.SkipIfProviderIsNotHealthy(tt)

	emulator.once.Do(func() {
		// The outcome is cached for the whole test binary, so the start must
		// not depend on the context of the test that happens to come first:
		// that test ending or being cancelled would fail every later test.
		ctx, cancel := context.WithTimeout(context.Background(), emulatorStartTimeout)
		defer cancel()
		emulator.ctr, emulator.err = tcpubsub.Run(ctx, EmulatorImage)
	})
	if emulator.err != nil {
		t.Fatalf("start Pub/Sub emulator container %s: %v", EmulatorImage, emulator.err)
	}
	return emulator.ctr.ProjectID(), []option.ClientOption{
		option.WithEndpoint(emulator.ctr.URI()),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}
}

// EmulatorClient returns a Pub/Sub client connected to the shared emulator,
// closed when the test ends.
func EmulatorClient(t testing.TB) *pubsub.Client {
	t.Helper()
	project, opts := EmulatorProject(t)
	client, err := pubsub.NewClient(t.Context(), project, opts...)
	if err != nil {
		t.Fatalf("connect to the Pub/Sub emulator: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Pub/Sub client: %v", err)
		}
	})
	return client
}

// TerminateEmulator stops the shared emulator if a test started it. Call it
// from TestMain after m.Run.
func TerminateEmulator() error {
	if emulator.ctr == nil {
		return nil
	}
	return testcontainers.TerminateContainer(emulator.ctr)
}

var resourceSeq atomic.Int64

// Fixture is a topic and a subscription attached to it, unique to one test.
type Fixture struct {
	Client       *pubsub.Client
	Topic        string // full resource name
	Subscription string // full resource name
}

// NewFixture creates a topic and a subscription with message ordering enabled.
func NewFixture(t testing.TB, client *pubsub.Client) Fixture {
	t.Helper()
	id := fmt.Sprintf("t%d-%d", time.Now().UnixNano(), resourceSeq.Add(1))
	f := Fixture{
		Client:       client,
		Topic:        fmt.Sprintf("projects/%s/topics/%s", client.Project(), id),
		Subscription: fmt.Sprintf("projects/%s/subscriptions/%s", client.Project(), id),
	}
	if _, err := client.TopicAdminClient.CreateTopic(t.Context(), &pubsubpb.Topic{Name: f.Topic}); err != nil {
		t.Fatalf("create topic %s: %v", f.Topic, err)
	}
	if _, err := client.SubscriptionAdminClient.CreateSubscription(t.Context(), &pubsubpb.Subscription{
		Name:                  f.Subscription,
		Topic:                 f.Topic,
		AckDeadlineSeconds:    int32(AckDeadline / time.Second),
		EnableMessageOrdering: true,
	}); err != nil {
		t.Fatalf("create subscription %s: %v", f.Subscription, err)
	}
	return f
}

// Publish publishes m to the fixture's topic and returns its server-assigned
// message ID.
func (f Fixture) Publish(t testing.TB, m *pubsub.Message) string {
	t.Helper()
	p := f.Client.Publisher(f.Topic)
	p.EnableMessageOrdering = m.OrderingKey != ""
	defer p.Stop()
	id, err := p.Publish(t.Context(), m).Get(t.Context())
	if err != nil {
		t.Fatalf("publish to %s: %v", f.Topic, err)
	}
	return id
}
