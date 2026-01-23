package error

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func TestEncode(t *testing.T) {
	testErr := errors.New("test error message")

	var buf bytes.Buffer
	err := Encode(&buf, testErr)
	require.NoError(t, err)

	// Decode the response to verify
	resp, err := cr.Decode(&buf)
	require.NoError(t, err)

	errVal, err := resp.Get("error")
	require.NoError(t, err)
	assert.Equal(t, "test error message", errVal)
}

func TestEncode_DifferentErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "simple error",
			err:      errors.New("simple"),
			expected: "simple",
		},
		{
			name:     "empty error message",
			err:      errors.New(""),
			expected: "",
		},
		{
			name:     "long error message",
			err:      errors.New("this is a very long error message that contains many characters"),
			expected: "this is a very long error message that contains many characters",
		},
		{
			name:     "error with special characters",
			err:      errors.New("error: something went wrong! @#$%^&*()"),
			expected: "error: something went wrong! @#$%^&*()",
		},
		{
			name:     "error with newlines",
			err:      errors.New("line1\nline2\nline3"),
			expected: "line1\nline2\nline3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := Encode(&buf, tc.err)
			require.NoError(t, err)

			resp, err := cr.Decode(&buf)
			require.NoError(t, err)

			errVal, err := resp.Get("error")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, errVal)
		})
	}
}

func TestEncode_WrappedError(t *testing.T) {
	innerErr := errors.New("inner error")
	wrappedErr := errors.New("wrapped: " + innerErr.Error())

	var buf bytes.Buffer
	err := Encode(&buf, wrappedErr)
	require.NoError(t, err)

	resp, err := cr.Decode(&buf)
	require.NoError(t, err)

	errVal, err := resp.Get("error")
	require.NoError(t, err)
	assert.Contains(t, errVal.(string), "inner error")
}

func TestEncode_FormattedError(t *testing.T) {
	formattedErr := errors.New("failed with code 42")

	var buf bytes.Buffer
	err := Encode(&buf, formattedErr)
	require.NoError(t, err)

	resp, err := cr.Decode(&buf)
	require.NoError(t, err)

	errVal, err := resp.Get("error")
	require.NoError(t, err)
	assert.Equal(t, "failed with code 42", errVal)
}

func TestEncode_UnicodeError(t *testing.T) {
	unicodeErr := errors.New("错误消息 🚨")

	var buf bytes.Buffer
	err := Encode(&buf, unicodeErr)
	require.NoError(t, err)

	resp, err := cr.Decode(&buf)
	require.NoError(t, err)

	errVal, err := resp.Get("error")
	require.NoError(t, err)
	assert.Equal(t, "错误消息 🚨", errVal)
}
