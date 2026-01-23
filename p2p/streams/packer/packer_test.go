package packer

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)

	p := New(magic, version)
	assert.NotNil(t, p, "New should return a non-nil Packer")
}

func TestSendAndRecv(t *testing.T) {
	magic := Magic{0xAB, 0xCD}
	version := Version(1)
	p := New(magic, version)

	testData := []byte("hello world")

	// Send
	var buf bytes.Buffer
	err := p.Send(0, &buf, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err, "Send should not return an error")

	// Recv
	var out bytes.Buffer
	channel, n, err := p.Recv(&buf, &out)
	require.NoError(t, err, "Recv should not return an error")

	assert.Equal(t, Channel(0), channel, "Channel should be 0")
	assert.Equal(t, int64(len(testData)), n, "Received length should match")
	assert.Equal(t, testData, out.Bytes(), "Received data should match sent data")
}

func TestSendAndRecv_DifferentChannels(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	channels := []Channel{0, 1, 127, 255}

	for _, ch := range channels {
		t.Run("channel_"+string(rune(ch)), func(t *testing.T) {
			testData := []byte("test data")
			var buf bytes.Buffer

			err := p.Send(ch, &buf, bytes.NewReader(testData), int64(len(testData)))
			require.NoError(t, err)

			var out bytes.Buffer
			receivedCh, _, err := p.Recv(&buf, &out)
			require.NoError(t, err)

			assert.Equal(t, ch, receivedCh, "Channel should match")
		})
	}
}

func TestRecv_WrongMagic(t *testing.T) {
	magic1 := Magic{0x01, 0x02}
	magic2 := Magic{0x03, 0x04}
	version := Version(1)

	p1 := New(magic1, version)
	p2 := New(magic2, version)

	testData := []byte("test")
	var buf bytes.Buffer

	// Send with magic1
	err := p1.Send(0, &buf, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Recv with magic2 should fail
	var out bytes.Buffer
	_, _, err = p2.Recv(&buf, &out)
	assert.Error(t, err, "Recv should fail with wrong magic")
	assert.Contains(t, err.Error(), "wrong packer magic")
}

func TestRecv_WrongVersion(t *testing.T) {
	magic := Magic{0x01, 0x02}
	v1 := Version(1)
	v2 := Version(2)

	p1 := New(magic, v1)
	p2 := New(magic, v2)

	testData := []byte("test")
	var buf bytes.Buffer

	// Send with v1
	err := p1.Send(0, &buf, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Recv with v2 should fail
	var out bytes.Buffer
	_, _, err = p2.Recv(&buf, &out)
	assert.Error(t, err, "Recv should fail with wrong version")
	assert.Contains(t, err.Error(), "wrong packer version")
}

func TestStream(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	testData := []byte("streaming data that is longer than usual")
	var buf bytes.Buffer

	// Stream the data
	n, err := p.Stream(0, &buf, bytes.NewReader(testData), 16)
	assert.Equal(t, io.EOF, err, "Stream should return EOF when done")
	assert.Equal(t, int64(len(testData)), n, "Stream should write all bytes")

	// Read streamed data in chunks
	var out bytes.Buffer
	for {
		ch, n, err := p.Recv(&buf, &out)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv failed: %v", err)
		}
		assert.Equal(t, Channel(0), ch)
		_ = n
	}

	assert.Equal(t, testData, out.Bytes(), "Streamed data should match original")
}

func TestSendClose(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version).(*packer)

	t.Run("with EOF", func(t *testing.T) {
		var buf bytes.Buffer
		err := p.SendClose(0, &buf, io.EOF)
		require.NoError(t, err)

		var out bytes.Buffer
		_, _, err = p.Recv(&buf, &out)
		assert.Equal(t, io.EOF, err, "Recv should return EOF")
	})

	t.Run("with nil error", func(t *testing.T) {
		var buf bytes.Buffer
		err := p.SendClose(0, &buf, nil)
		require.NoError(t, err)

		var out bytes.Buffer
		_, _, err = p.Recv(&buf, &out)
		assert.Equal(t, io.EOF, err, "Recv should return EOF")
	})

	t.Run("with custom error", func(t *testing.T) {
		var buf bytes.Buffer
		customErr := errors.New("custom error message")
		err := p.SendClose(0, &buf, customErr)
		require.NoError(t, err)

		var out bytes.Buffer
		_, _, err = p.Recv(&buf, &out)
		assert.Error(t, err, "Recv should return an error")
		assert.Contains(t, err.Error(), "custom error message")
	})
}

func TestNext(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	testData := []byte("test data for next")
	var buf bytes.Buffer

	err := p.Send(5, &buf, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Read headers only
	ch, length, err := p.Next(&buf)
	require.NoError(t, err)

	assert.Equal(t, Channel(5), ch, "Channel should match")
	assert.Equal(t, int64(len(testData)), length, "Length should match")
}

func TestNext_WrongMagic(t *testing.T) {
	magic1 := Magic{0x01, 0x02}
	magic2 := Magic{0x03, 0x04}
	version := Version(1)

	p1 := New(magic1, version)
	p2 := New(magic2, version)

	testData := []byte("test")
	var buf bytes.Buffer

	err := p1.Send(0, &buf, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	_, _, err = p2.Next(&buf)
	assert.Error(t, err, "Next should fail with wrong magic")
	assert.Contains(t, err.Error(), "wrong packer magic")
}

func TestNext_WrongVersion(t *testing.T) {
	magic := Magic{0x01, 0x02}
	v1 := Version(1)
	v2 := Version(2)

	p1 := New(magic, v1)
	p2 := New(magic, v2)

	testData := []byte("test")
	var buf bytes.Buffer

	err := p1.Send(0, &buf, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	_, _, err = p2.Next(&buf)
	assert.Error(t, err, "Next should fail with wrong version")
	assert.Contains(t, err.Error(), "wrong packer version")
}

func TestNext_Close(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version).(*packer)

	t.Run("close with no error", func(t *testing.T) {
		var buf bytes.Buffer
		err := p.SendClose(3, &buf, nil)
		require.NoError(t, err)

		ch, _, err := p.Next(&buf)
		assert.Equal(t, io.EOF, err)
		assert.Equal(t, Channel(3), ch)
	})

	t.Run("close with error message", func(t *testing.T) {
		var buf bytes.Buffer
		err := p.SendClose(3, &buf, errors.New("test error"))
		require.NoError(t, err)

		_, _, err = p.Next(&buf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "test error")
	})
}

func TestSend_ShortWrite(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	// Create a reader that returns less data than specified length
	shortReader := bytes.NewReader([]byte("short"))
	var buf bytes.Buffer

	// Try to send with longer length than actual data
	err := p.Send(0, &buf, shortReader, 100)
	assert.Error(t, err, "Send should fail when reader provides less data than specified")
}

func TestRecv_EmptyReader(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	var emptyBuf bytes.Buffer
	var out bytes.Buffer

	_, _, err := p.Recv(&emptyBuf, &out)
	assert.Error(t, err, "Recv should fail on empty reader")
}

func TestLargeData(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	// Create large test data (100KB)
	largeData := make([]byte, 100*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	var buf bytes.Buffer
	err := p.Send(0, &buf, bytes.NewReader(largeData), int64(len(largeData)))
	require.NoError(t, err)

	var out bytes.Buffer
	_, n, err := p.Recv(&buf, &out)
	require.NoError(t, err)

	assert.Equal(t, int64(len(largeData)), n)
	assert.Equal(t, largeData, out.Bytes())
}

func TestEmptyData(t *testing.T) {
	magic := Magic{0x01, 0x02}
	version := Version(1)
	p := New(magic, version)

	emptyData := []byte{}

	var buf bytes.Buffer
	err := p.Send(0, &buf, bytes.NewReader(emptyData), 0)
	require.NoError(t, err)

	var out bytes.Buffer
	ch, n, err := p.Recv(&buf, &out)
	require.NoError(t, err)

	assert.Equal(t, Channel(0), ch)
	assert.Equal(t, int64(0), n)
	assert.Equal(t, emptyData, out.Bytes())
}

func TestTypeConstants(t *testing.T) {
	assert.Equal(t, Type(0), TypeData)
	assert.Equal(t, Type(1), TypeClose)
}
