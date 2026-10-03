package event

import (
	"encoding/base64"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	pubsub "cloud.google.com/go/pubsub/v2"
	gocmp "github.com/google/go-cmp/cmp"
)

// validMetaKey is the key shape Claude Code keeps; it silently drops any other.
var validMetaKey = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

var testPublishTime = time.Date(2026, 10, 3, 6, 30, 15, 123456789, time.FixedZone("JST", 9*60*60))

// baseMeta returns the meta every event carries, plus extra.
func baseMeta(extra map[string]string) map[string]string {
	meta := map[string]string{
		KeyMessageID:    "msg-1",
		KeyPublishTime:  "2026-10-02T21:30:15.123456789Z",
		KeySubscription: "projects/p/subscriptions/s",
	}
	maps.Copy(meta, extra)
	return meta
}

func TestFromMessage(t *testing.T) {
	invalidUTF8 := []byte{0xff, 0xfe, 0x00, 0x01, 'a'}
	tests := map[string]struct {
		data            []byte
		attributes      map[string]string
		orderingKey     string
		deliveryAttempt *int
		opts            Options
		want            Event
	}{
		"success: UTF-8 data is the content": {
			data: []byte("deploy finished: 3 services"),
			want: Event{Content: "deploy finished: 3 services", Meta: baseMeta(nil)},
		},
		"success: multi-byte UTF-8 data is kept as is": {
			data: []byte("ビルド成功 ✅"),
			want: Event{Content: "ビルド成功 ✅", Meta: baseMeta(nil)},
		},
		"success: invalid UTF-8 is base64 encoded": {
			data: invalidUTF8,
			want: Event{
				Content: base64.StdEncoding.EncodeToString(invalidUTF8),
				Meta:    baseMeta(map[string]string{KeyEncoding: "base64"}),
			},
		},
		"success: empty data is empty content": {
			data: nil,
			want: Event{Content: "", Meta: baseMeta(nil)},
		},
		"success: ordering key and delivery attempt are reported when set": {
			data:            []byte("x"),
			orderingKey:     "tenant-7",
			deliveryAttempt: new(3),
			want: Event{Content: "x", Meta: baseMeta(map[string]string{
				KeyOrderingKey:     "tenant-7",
				KeyDeliveryAttempt: "3",
			})},
		},
		"success: content under the cap is not truncated": {
			data: []byte("12345"),
			opts: Options{MaxContentBytes: 5},
			want: Event{Content: "12345", Meta: baseMeta(nil)},
		},
		"success: ASCII content over the cap is cut at the cap": {
			data: []byte("1234567890"),
			opts: Options{MaxContentBytes: 4},
			want: Event{Content: "1234", Meta: baseMeta(map[string]string{KeyTruncated: "true", KeyOriginalBytes: "10"})},
		},
		"success: truncation backs off to a rune boundary": {
			// "あ" and "い" are 3 bytes each; a 4-byte cap would split "い".
			data: []byte("あいう"),
			opts: Options{MaxContentBytes: 4},
			want: Event{Content: "あ", Meta: baseMeta(map[string]string{KeyTruncated: "true", KeyOriginalBytes: "9"})},
		},
		"success: truncation exactly on a rune boundary keeps the rune": {
			data: []byte("あいう"),
			opts: Options{MaxContentBytes: 6},
			want: Event{Content: "あい", Meta: baseMeta(map[string]string{KeyTruncated: "true", KeyOriginalBytes: "9"})},
		},
		"success: base64 content is cut on a 4-character boundary": {
			data: invalidUTF8, // encodes to 8 characters
			opts: Options{MaxContentBytes: 7},
			want: Event{
				Content: base64.StdEncoding.EncodeToString(invalidUTF8)[:4],
				Meta: baseMeta(map[string]string{
					KeyEncoding:      "base64",
					KeyTruncated:     "true",
					KeyOriginalBytes: "5",
				}),
			},
		},
		"success: base64 content at the minimum cap keeps one 4-character quantum": {
			data: invalidUTF8,
			opts: Options{MaxContentBytes: MinContentBytes},
			want: Event{
				Content: base64.StdEncoding.EncodeToString(invalidUTF8[:3]),
				Meta: baseMeta(map[string]string{
					KeyEncoding:      "base64",
					KeyTruncated:     "true",
					KeyOriginalBytes: "5",
				}),
			},
		},
		"success: UTF-8 content at the minimum cap keeps a 4-byte rune": {
			data: []byte("😀😀"),
			opts: Options{MaxContentBytes: MinContentBytes},
			want: Event{Content: "😀", Meta: baseMeta(map[string]string{KeyTruncated: "true", KeyOriginalBytes: "8"})},
		},
		"success: attribute keys with punctuation and non-ASCII are sanitised": {
			data: []byte("x"),
			attributes: map[string]string{
				"event.type":    "push",
				"repo/name":     "zchee/mcp-servers",
				"build-id":      "b-1",
				"ステータス":         "ok",
				"already_valid": "yes",
			},
			want: Event{Content: "x", Meta: baseMeta(map[string]string{
				"attr_event_type":    "push",
				"attr_repo_name":     "zchee/mcp-servers",
				"attr_build_id":      "b-1",
				"attr______":         "ok",
				"attr_already_valid": "yes",
			})},
		},
		"success: keys colliding after sanitising are numbered in sorted key order": {
			data: []byte("x"),
			attributes: map[string]string{
				"a.b":   "dot",
				"a-b":   "dash",
				"a_b":   "underscore",
				"a_b_2": "literal suffix",
			},
			// Sorted keys: "a-b" < "a.b" < "a_b" < "a_b_2".
			want: Event{Content: "x", Meta: baseMeta(map[string]string{
				"attr_a_b":     "dash",
				"attr_a_b_2":   "dot",
				"attr_a_b_3":   "underscore",
				"attr_a_b_2_2": "literal suffix",
			})},
		},
		"success: an attribute named like a built-in cannot overwrite it": {
			data: []byte("x"),
			attributes: map[string]string{
				KeyMessageID: "forged",
				KeyTruncated: "true",
			},
			want: Event{Content: "x", Meta: baseMeta(map[string]string{
				"attr_message_id": "forged",
				"attr_truncated":  "true",
			})},
		},
		"success: allowlist forwards only listed attributes": {
			data: []byte("x"),
			attributes: map[string]string{
				"keep.me": "1",
				"drop":    "2",
				"also":    "3",
			},
			opts: Options{Attributes: []string{"keep.me", "also", "absent"}},
			want: Event{Content: "x", Meta: baseMeta(map[string]string{
				"attr_keep_me": "1",
				"attr_also":    "3",
			})},
		},
		"success: empty non-nil allowlist forwards no attributes": {
			data:       []byte("x"),
			attributes: map[string]string{"k": "v"},
			opts:       Options{Attributes: []string{}},
			want:       Event{Content: "x", Meta: baseMeta(nil)},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.opts.Subscription == "" {
				tt.opts.Subscription = "projects/p/subscriptions/s"
			}
			m := &pubsub.Message{
				ID:              "msg-1",
				Data:            tt.data,
				Attributes:      tt.attributes,
				PublishTime:     testPublishTime,
				OrderingKey:     tt.orderingKey,
				DeliveryAttempt: tt.deliveryAttempt,
			}

			got := FromMessage(m, tt.opts)

			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("FromMessage (-want +got):\n%s", diff)
			}
			for k := range got.Meta {
				if !validMetaKey.MatchString(k) {
					t.Errorf("meta key %q does not match %s; Claude Code would drop it", k, validMetaKey)
				}
			}
			if !utf8.ValidString(got.Content) {
				t.Errorf("content %q is not valid UTF-8", got.Content)
			}
			if limit := tt.opts.MaxContentBytes; limit > 0 && len(got.Content) > limit {
				t.Errorf("content is %d bytes, over the %d-byte cap", len(got.Content), limit)
			}
			if got.Meta[KeyEncoding] == "base64" {
				if _, err := base64.StdEncoding.DecodeString(got.Content); err != nil {
					t.Errorf("base64 content %q does not decode: %v", got.Content, err)
				}
			}
		})
	}
}

func TestFromMessageIsDeterministic(t *testing.T) {
	tests := map[string]struct {
		attributes map[string]string
	}{
		"success: keys that all sanitise to the same name": {
			attributes: map[string]string{"a.b": "1", "a-b": "2", "a_b": "3", "a/b": "4", "a b": "5", "a:b": "6"},
		},
		"success: sanitised keys colliding with a numbered literal key": {
			attributes: map[string]string{"x.y": "1", "x-y": "2", "x_y_2": "3", "x_y": "4"},
		},
		"success: distinct keys with nothing to sanitise": {
			attributes: map[string]string{"alpha": "1", "beta": "2", "gamma": "3"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			m := &pubsub.Message{ID: "1", Data: []byte("x"), Attributes: tt.attributes, PublishTime: testPublishTime}
			first := FromMessage(m, Options{})
			// Map iteration order is randomised per range statement, so
			// repeating the mapping exercises different attribute orders.
			for i := range 100 {
				if diff := gocmp.Diff(first, FromMessage(m, Options{})); diff != "" {
					t.Fatalf("mapping #%d differs from the first (-first +got):\n%s", i, diff)
				}
			}
		})
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := map[string]struct {
		key  string
		want string
	}{
		"success: valid key is unchanged":     {key: "Abc_123", want: "Abc_123"},
		"success: punctuation is replaced":    {key: "a.b/c-d:e f", want: "a_b_c_d_e_f"},
		"success: each non-ASCII rune is one": {key: "é✅", want: "__"},
		"success: empty key stays empty":      {key: "", want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, SanitizeKey(tt.key)); diff != "" {
				t.Errorf("SanitizeKey(%q) (-want +got):\n%s", tt.key, diff)
			}
		})
	}
}

func TestInstructions(t *testing.T) {
	tests := map[string]struct {
		subscription string
	}{
		"success: names the subscription and every meta key": {subscription: "projects/p/subscriptions/alerts"},
		"success: other subscription":                        {subscription: "projects/other-proj/subscriptions/deploys"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := Instructions(tt.subscription)
			for _, want := range []string{tt.subscription, "one-way", "untrusted", "duplicate delivery", "ordering_key are untrusted", KeyMessageID, KeyPublishTime, KeySubscription, KeyOrderingKey, KeyDeliveryAttempt, KeyEncoding, KeyTruncated, KeyOriginalBytes, AttributePrefix} {
				if !strings.Contains(got, want) {
					t.Errorf("Instructions(%q) does not mention %q:\n%s", tt.subscription, want, got)
				}
			}
		})
	}
}

func BenchmarkFromMessage(b *testing.B) {
	attrs := map[string]string{"event.type": "push", "repo/name": "zchee/mcp-servers", "build-id": "b-1", "status": "ok"}
	benchmarks := map[string]struct {
		data []byte
		opts Options
	}{
		"utf8_1KiB":            {data: []byte(strings.Repeat("abcdefgh", 128))},
		"utf8_1MiB_truncated":  {data: []byte(strings.Repeat("あいうえお", 1<<16)), opts: Options{MaxContentBytes: DefaultMaxContentBytes}},
		"binary_64KiB_encoded": {data: slices.Repeat([]byte{0xff, 0x00}, 32<<10), opts: Options{MaxContentBytes: DefaultMaxContentBytes}},
	}
	for name, bm := range benchmarks {
		b.Run(name, func(b *testing.B) {
			m := &pubsub.Message{ID: "1", Data: bm.data, Attributes: attrs, PublishTime: testPublishTime}
			bm.opts.Subscription = "projects/p/subscriptions/s"
			b.SetBytes(int64(len(bm.data)))
			b.ReportAllocs()
			for b.Loop() {
				_ = FromMessage(m, bm.opts)
			}
		})
	}
}
