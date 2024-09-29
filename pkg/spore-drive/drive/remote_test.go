package drive

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/mock"
	"github.com/taubyte/tau/pkg/mycelium/command"
	cmocks "github.com/taubyte/tau/pkg/mycelium/command/mocks"

	"github.com/taubyte/tau/pkg/mycelium/host/mocks"
	"gotest.tools/v3/assert"
)

func TestNewRemote(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	_, err := newRemote(context.TODO(), h)
	assert.NilError(t, err)
}

func TestRemoteSudo(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	ctx := context.TODO()
	sess := &cmocks.RemoteSession{}
	sess.On("Close").Return(nil)
	sess.On("CombinedOutput", "sudo \"echo\"").Return([]byte(""), nil)
	mockCmd, _ := command.New(ctx, sess, "sudo", command.Args("echo"))

	h.On("Command", ctx, "sudo", mock.Anything).Return(mockCmd, nil)

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	_, err = r.Sudo(ctx, "echo")
	assert.NilError(t, err)

	h.AssertCalled(t, "Command", ctx, "sudo", mock.Anything)
}

func TestRemoteExecute(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	ctx := context.TODO()
	sess := &cmocks.RemoteSession{}
	sess.On("Close").Return(nil)
	sess.On("CombinedOutput", "echo \"hello\"").Return([]byte("hello"), nil)
	mockCmd, _ := command.New(ctx, sess, "echo", command.Args("hello"))

	h.On("Command", ctx, "echo", mock.Anything).Return(mockCmd, nil)

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	_, err = r.Execute(ctx, "echo", "hello")
	assert.NilError(t, err)

	h.AssertCalled(t, "Command", ctx, "echo", mock.Anything)
}

func TestRemoteOpen(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	f, _ := mfs.OpenFile("hello.txt", os.O_CREATE, 0640)
	f.WriteString("world")
	f.Close()

	ctx := context.TODO()

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	rd, err := r.Open("hello.txt")
	assert.NilError(t, err)

	data, err := io.ReadAll(rd)
	assert.NilError(t, err)
	rd.Close()

	assert.Equal(t, string(data), "world")
}

func TestRemoteOpenFail(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	ctx := context.TODO()

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	_, err = r.Open("hello.txt")
	assert.ErrorContains(t, err, "file does not exist")
}

func TestRemoteOpenFile(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	ctx := context.TODO()

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	rd, err := r.OpenFile("hello.txt", os.O_CREATE|os.O_RDWR, 0640)
	assert.NilError(t, err)

	_, err = io.WriteString(rd, "world")
	assert.NilError(t, err)

	rd.Close()

	rd, err = mfs.Open("hello.txt")
	assert.NilError(t, err)

	data, err := io.ReadAll(rd)
	assert.NilError(t, err)
	rd.Close()

	assert.Equal(t, string(data), "world")
}

func TestRemoteRemove(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	f, _ := mfs.OpenFile("hello.txt", os.O_CREATE, 0640)
	f.WriteString("world")
	f.Close()

	ctx := context.TODO()

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	err = r.Remove("hello.txt")
	assert.NilError(t, err)

	_, err = mfs.Stat("hello.txt")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRemoteRemoveAll(t *testing.T) {
	h := new(mocks.Host)
	mfs := afero.NewMemMapFs()
	h.On("Fs", mock.Anything, mock.Anything).Return(mfs, nil)

	assert.NilError(t, mfs.Mkdir("/dir", 0750))

	f, _ := mfs.OpenFile("/dir/hello.txt", os.O_CREATE, 0640)
	f.WriteString("world")
	f.Close()

	f, _ = mfs.OpenFile("/dir/hello2.txt", os.O_CREATE, 0640)
	f.WriteString("worlD")
	f.Close()

	ctx := context.TODO()

	r, err := newRemote(ctx, h)
	assert.NilError(t, err)

	err = r.RemoveAll("/dir")
	assert.NilError(t, err)

	_, err = mfs.Stat("/dir/hello.txt")
	assert.ErrorIs(t, err, os.ErrNotExist)

	_, err = mfs.Stat("/dir/hello2.txt")
	assert.ErrorIs(t, err, os.ErrNotExist)

	_, err = mfs.Stat("/dir")
	assert.ErrorIs(t, err, os.ErrNotExist)
}
