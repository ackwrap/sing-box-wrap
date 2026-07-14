package pool

import (
	"bytes"
	"sync"
)

const RelayBufferSize = 20 * 1024

var bufferPool = sync.Pool{New: func() any { return &bytes.Buffer{} }}

func Get(size int) []byte { return make([]byte, size) }
func Put([]byte) error    { return nil }

func GetBuffer() *bytes.Buffer { return bufferPool.Get().(*bytes.Buffer) }

func PutBuffer(buffer *bytes.Buffer) {
	buffer.Reset()
	bufferPool.Put(buffer)
}
