package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestMessaging_WithBridges(t *testing.T) {
	obj := object.New[object.Refrence]()
	messagingObj, _ := obj.CreatePath("messaging")
	msgSel := messagingObj.Child("myMessaging")
	msgSel.Set("id", "msg-id-123")
	msgSel.Set("websocket", true)
	msgSel.Set("mqtt", false)

	transformer := Messaging()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify messaging renamed by ID
	renamedMsgSel := messagingObj.Child("msg-id-123")

	// Verify websocket moved to webSocket (camelCase)
	webSocket, err := renamedMsgSel.Get("webSocket")
	assert.NilError(t, err)
	assert.Equal(t, webSocket.(bool), true)

	// Verify name set
	name, err := renamedMsgSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myMessaging")

	// Verify indexed
	indexPath := "messaging/myMessaging"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "msg-id-123")

}

func TestMessaging_NoMessaging(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Messaging()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestMessaging_MultipleMessaging(t *testing.T) {
	obj := object.New[object.Refrence]()
	messagingObj, _ := obj.CreatePath("messaging")

	msg1 := messagingObj.Child("messaging1")
	msg1.Set("id", "id1")
	msg1.Set("websocket", true)

	msg2 := messagingObj.Child("messaging2")
	msg2.Set("id", "id2")
	msg2.Set("mqtt", true)

	transformer := Messaging()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both messaging renamed
	_, err = messagingObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = messagingObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("messaging/messaging1").Exist())
	assert.Assert(t, ctx.Store().String("messaging/messaging2").Exist())
}
