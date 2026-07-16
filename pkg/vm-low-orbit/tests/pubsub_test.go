package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// The pubsub guest, when hit over HTTP, drives the plugin's publish/subscribe
// host functions. We assert the plugin passed the guest's channel + payload
// through to the (mocked) node service unchanged — i.e. the host ABI is wired
// correctly end to end.

func TestPubsubPublish(t *testing.T) {
	before := len(pubsubMock.published)

	req := httptest.NewRequest("GET", "/pubsub?name=actuallypublish", nil)
	_, code := guestCall(t, context.Background(), "pubsub", "pubsubtest", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d, want 0", code)
	}

	_, published := pubsubMock.snapshot()
	if len(published) != before+1 {
		t.Fatalf("published %d messages, want %d", len(published)-before, 1)
	}
	got := published[len(published)-1]
	if got.channel != "someChannel" {
		t.Fatalf("channel = %q, want %q", got.channel, "someChannel")
	}
	if string(got.data) != "Hello, world" {
		t.Fatalf("data = %q, want %q", got.data, "Hello, world")
	}
}

func TestPubsubSubscribe(t *testing.T) {
	before := len(pubsubMock.subs)

	req := httptest.NewRequest("GET", "/pubsub?name=pubstuff", nil)
	_, code := guestCall(t, context.Background(), "pubsub", "pubsubtest", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d, want 0", code)
	}

	subs, _ := pubsubMock.snapshot()
	if len(subs) != before+1 {
		t.Fatalf("subscribed %d channels, want %d", len(subs)-before, 1)
	}
	if last := subs[len(subs)-1]; last != "someChannel" {
		t.Fatalf("subscribed channel = %q, want %q", last, "someChannel")
	}
}
