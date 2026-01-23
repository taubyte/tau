package router

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func TestNew(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	assert.NotNil(t, r)
	assert.NotNil(t, r.staticRoutes)
}

func TestAddStatic(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{"status": "ok"}, nil
	}

	err := r.AddStatic("testCmd", handler, nil)
	require.NoError(t, err)

	// Verify handler was added
	_, exists := r.staticRoutes["testCmd"]
	assert.True(t, exists)
}

func TestAddStatic_NilHandler(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	err := r.AddStatic("testCmd", nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot add nil handler")
}

func TestAddStatic_DuplicateCommand(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{}, nil
	}

	err := r.AddStatic("testCmd", handler, nil)
	require.NoError(t, err)

	// Try to add same command again
	err = r.AddStatic("testCmd", handler, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestAddStatic_WithStreamHandler(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	cmdHandler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{"status": "ok"}, nil
	}

	streamHandler := func(ctx context.Context, rw io.ReadWriter) {
		// Stream upgrade handler
	}

	err := r.AddStatic("upgradeCmd", cmdHandler, streamHandler)
	require.NoError(t, err)

	handlers, exists := r.staticRoutes["upgradeCmd"]
	assert.True(t, exists)
	assert.NotNil(t, handlers.std)
	assert.NotNil(t, handlers.stream)
}

func TestHandle_NilCommand(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	resp, upgrade, err := r.handle(nil)
	assert.Nil(t, resp)
	assert.Nil(t, upgrade)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil command")
}

func TestHandle_CommandWithNoConnection(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	// Create a command with no connection (internal conn field is nil)
	cmd := command.New("unknownCmd", command.Body{})

	// handle will fail at Connection() call since conn is nil
	_, _, err := r.handle(cmd)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no connection found")
}

func TestRouter_MultipleCommands(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}

	for _, cmdName := range commands {
		handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
			return cr.Response{"command": cmdName}, nil
		}
		err := r.AddStatic(cmdName, handler, nil)
		require.NoError(t, err)
	}

	// Verify all commands were registered
	for _, cmdName := range commands {
		_, exists := r.staticRoutes[cmdName]
		assert.True(t, exists, "Command %s should be registered", cmdName)
	}
}

func TestCommandHandler_ReturnsError(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	expectedErr := errors.New("handler error")
	handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return nil, expectedErr
	}

	err := r.AddStatic("errorCmd", handler, nil)
	require.NoError(t, err)
}

func TestCommandHandler_ReturnsResponse(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{
			"status": "success",
			"data":   "test data",
		}, nil
	}

	err := r.AddStatic("successCmd", handler, nil)
	require.NoError(t, err)
}

func TestStreamHandler_Type(t *testing.T) {
	var sh StreamHandler = func(ctx context.Context, rw io.ReadWriter) {
		// Implementation
	}
	assert.NotNil(t, sh)
}

func TestCommandHandler_Type(t *testing.T) {
	var ch CommandHandler = func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return nil, nil
	}
	assert.NotNil(t, ch)
}

func TestRouter_EmptyRoutes(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	assert.Empty(t, r.staticRoutes)
}

func TestAddStatic_SpecialCharactersInCommand(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	specialCommands := []string{
		"cmd-with-dashes",
		"cmd_with_underscores",
		"cmd.with.dots",
		"cmd/with/slashes",
		"cmd:with:colons",
	}

	handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{}, nil
	}

	for _, cmdName := range specialCommands {
		t.Run(cmdName, func(t *testing.T) {
			err := r.AddStatic(cmdName, handler, nil)
			require.NoError(t, err)

			_, exists := r.staticRoutes[cmdName]
			assert.True(t, exists)
		})
	}
}

func TestAddStatic_EmptyCommandName(t *testing.T) {
	svr := &streams.StreamManger{}
	r := New(svr)

	handler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{}, nil
	}

	// Empty command name should still work (though not recommended)
	err := r.AddStatic("", handler, nil)
	require.NoError(t, err)

	_, exists := r.staticRoutes[""]
	assert.True(t, exists)
}
