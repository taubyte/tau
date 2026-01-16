package transform

import (
	"context"
	"io"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

type mockTransformer struct {
	transformValue int
	err            error
}

func (m *mockTransformer) Process(c Context[int], o object.Object[int]) (object.Object[int], error) {
	currentValue := o.Get("testKey")
	o.Set("testKey", currentValue+m.transformValue)
	return o, m.err
}

func TestPipe(t *testing.T) {
	initialValue := 0

	// Create an initial object
	obj := object.New[int]()
	obj.Set("testKey", initialValue)

	// Create mock transformers
	t1 := Transformer[int](&mockTransformer{transformValue: 1})
	t2 := Transformer[int](&mockTransformer{transformValue: 2})
	t3 := Transformer[int](&mockTransformer{transformValue: 3})

	// Use Pipe function to sequentially apply transformers
	pipedObj, err := Pipe(NewContext[int](context.Background()), obj, t1, t2, t3)
	assert.NilError(t, err)

	// Check the transformed value
	assert.Equal(t, pipedObj.Get("testKey"), initialValue+1+2+3)
}

func TestPipeError(t *testing.T) {
	initialValue := 0

	// Create an initial object
	obj := object.New[int]()
	obj.Set("testKey", initialValue)

	// Create mock transformers
	t1 := Transformer[int](&mockTransformer{transformValue: 1})
	t2 := Transformer[int](&mockTransformer{transformValue: 2, err: io.ErrUnexpectedEOF})
	t3 := Transformer[int](&mockTransformer{transformValue: 3})

	// Use Pipe function to sequentially apply transformers
	_, err := Pipe(NewContext[int](context.Background()), obj, t1, t2, t3)
	assert.Error(t, err, io.ErrUnexpectedEOF.Error())

}
