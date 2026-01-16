package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestMessaging_NoMessaging(t *testing.T) {
	messaging := Messaging()

	obj := object.New[object.Refrence]()
	// No messaging group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := messaging.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no messaging")
}

func TestMessaging_WithMessaging(t *testing.T) {
	messaging := Messaging()

	root := object.New[object.Refrence]()
	messagingObj := object.New[object.Refrence]()

	msg1 := object.New[object.Refrence]()
	msg1.Set("name", "my-messaging")
	msg1.Set("id", "msg-id-1")
	msg1.Set("webSocket", "ws://example.com")
	err := messagingObj.Child("msg-id-1").Add(msg1)
	assert.NilError(t, err)

	err = root.Child("messaging").Add(messagingObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := messaging.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultMessaging, err := result.Child("messaging").Object()
	assert.NilError(t, err)
	resultMsg1, err := resultMessaging.Child("my-messaging").Object()
	assert.NilError(t, err)

	// Should have moved webSocket to websocket
	websocket, err := resultMsg1.GetString("websocket")
	assert.NilError(t, err)
	assert.Equal(t, websocket, "ws://example.com")
}

func TestMessaging_MissingName(t *testing.T) {
	messaging := Messaging()

	root := object.New[object.Refrence]()
	messagingObj := object.New[object.Refrence]()

	msg1 := object.New[object.Refrence]()
	msg1.Set("id", "msg-id-1")
	// Missing name
	err := messagingObj.Child("msg-id-1").Add(msg1)
	assert.NilError(t, err)

	err = root.Child("messaging").Add(messagingObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	_, err = messaging.Process(ctx, root)
	assert.ErrorContains(t, err, "fetching name for messaging")
}
