//go:build dreaming

// Package pubsub_dreaming_test is the first end-to-end dreaming coverage for a
// Type:"pubsub" wasm function: a published message travels through the real
// substrate pubsub component (Subscribe/Publish/Lookup), into
// function.HandleMessage, CreatePubsubEvent, and the wasm handler, which
// republishes on a reply channel that the test observes directly.
package pubsub_dreaming_test

import (
	"context"
	"os"
	"path"
	"testing"
	"time"

	gopubsub "github.com/libp2p/go-libp2p-pubsub"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/pkg/tcc/interp/fixtures"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	pubsubComponent "github.com/taubyte/tau/services/substrate/components/pubsub"
	pubsubCommon "github.com/taubyte/tau/services/substrate/components/pubsub/common"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
)

const (
	testProjectId  = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"

	echoChannel  = "echochannel"
	replyChannel = "replychannel"
)

// TestPubsubFunction_Dreaming publishes a message on echoChannel and expects
// the compiled wasm function (assets/echo.go) to republish
// "echo:"+data on replyChannel.
func TestPubsubFunction_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {},
			"hoarder":   {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	fs, _, err := tcc.GenerateProject(testProjectId,
		&structureSpec.Messaging{
			Name:  "echoMessaging",
			Match: echoChannel,
		},
		// No function ever subscribes here - this channel only exists so the
		// wasm function's reply-publish has somewhere to Lookup into. Marked
		// WebSocket so CheckTns resolves a serviceable for it even though no
		// function's Channel matches; see pubsub/lookup.go's CheckTns: a
		// channel whose only match is a plain (non-websocket) Messaging entry
		// with no listening function fails Lookup with "no pubsub matches".
		&structureSpec.Messaging{
			Name:      "replyMessaging",
			Match:     replyChannel,
			WebSocket: true,
		},
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "pubsubEchoFunc",
			Type:    "pubsub",
			Channel: echoChannel,
			Call:    "pubsubEcho",
			Memory:  20 * 1024 * 1024,
			Source:  ".",
			Timeout: 1000000000,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err = u.RunFixture("injectProject", fs); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	compileStart := time.Now()
	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testFunctionId,
		Paths:      []string{path.Join(wd, "assets", "echo.go")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compiled pubsub function fixture in %s", time.Since(compileStart))

	// Reach the pubsub component the same way services/substrate/attach.go
	// does (pubSub.New(srv)) rather than reflecting into the substrate
	// service's private components struct. AddSubscription's subscription
	// registry (websocket/handler.go) is keyed by topic name at package
	// scope, and Node()/Tns()/Cache come from the shared substrate service,
	// so this exercises the same wiring the attached component uses.
	svc, err := pubsubComponent.New(u.Substrate())
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe to the reply topic directly on the raw node before
	// publishing, so the wasm function's reply can't race the test's
	// listener.
	replyMatcher := &pubsubCommon.MatchDefinition{Channel: replyChannel, Project: testProjectId}
	replies := make(chan []byte, 4)
	err = u.Substrate().Node().PubSubSubscribe(replyMatcher.String(),
		func(msg *gopubsub.Message) {
			message, err := pubsubCommon.NewMessage(msg, "")
			if err != nil {
				t.Logf("decoding reply message failed with: %s", err)
				return
			}
			replies <- message.GetData()
		},
		func(err error) {
			t.Logf("reply subscription error: %s", err)
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Bootstrap serving: nothing auto-subscribes substrate to a pubsub
	// channel - in production this is done by the wasm SDK's subscribe()
	// shim (pkg/vm-low-orbit/pubsub/subscribe.go) calling this same method
	// from inside a running function. A test has no running function yet,
	// so it calls Subscribe itself. TNS config propagation from
	// injectProject/compileFor is async, so retry until it resolves.
	var subErr error
	for i := 0; i < 60; i++ {
		subErr = svc.Subscribe(testProjectId, "", "test-subscriber", echoChannel)
		if subErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if subErr != nil {
		t.Fatalf("subscribing to %q never succeeded: %v", echoChannel, subErr)
	}

	// resource must differ from Subscribe's "test-subscriber" above - both
	// publish.go and subscribe.go key self-message filtering off it.
	if err := svc.Publish(context.Background(), testProjectId, "", "test-publisher", echoChannel, []byte("hello")); err != nil {
		t.Fatalf("publishing to %q failed with: %v", echoChannel, err)
	}

	select {
	case data := <-replies:
		if string(data) != "echo:hello" {
			t.Fatalf("expected reply %q got %q", "echo:hello", data)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for echo reply on " + replyChannel)
	}
}
