package obfs

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"net"
	"sync"

	"github.com/sagernet/sing-box/transport/clashssr/internal/pool"
)

func init() {
	register("random_head", newRandomHead, 0)
}

type randomHead struct {
	*Base
}

func newRandomHead(b *Base) Obfs {
	return &randomHead{Base: b}
}

type randomHeadConn struct {
	net.Conn
	*randomHead
	hasSentHeader bool
	rawTransSent  bool
	rawTransRecv  bool
	buf           []byte
	stateMutex    sync.Mutex
}

func (r *randomHead) StreamConn(c net.Conn) net.Conn {
	return &randomHeadConn{Conn: c, randomHead: r}
}

func (c *randomHeadConn) Read(b []byte) (int, error) {
	c.stateMutex.Lock()
	if c.rawTransRecv {
		c.stateMutex.Unlock()
		return c.Conn.Read(b)
	}
	c.stateMutex.Unlock()
	buf := pool.Get(pool.RelayBufferSize)
	defer pool.Put(buf)
	if _, err := c.Conn.Read(buf); err != nil {
		return 0, err
	}
	c.stateMutex.Lock()
	c.rawTransRecv = true
	if len(c.buf) > 0 {
		if err := writeAll(c.Conn, c.buf); err != nil {
			c.stateMutex.Unlock()
			return 0, err
		}
		c.buf = nil
	}
	c.rawTransSent = true
	c.stateMutex.Unlock()
	return c.Conn.Read(b)
}

func (c *randomHeadConn) Write(b []byte) (int, error) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	if c.rawTransSent {
		return c.Conn.Write(b)
	}
	c.buf = append(c.buf, b...)
	if !c.hasSentHeader {
		c.hasSentHeader = true
		dataLength := rand.Intn(96) + 4
		buf := pool.Get(dataLength + 4)
		defer pool.Put(buf)
		rand.Read(buf[:dataLength])
		binary.LittleEndian.PutUint32(buf[dataLength:], 0xffffffff-crc32.ChecksumIEEE(buf[:dataLength]))
		_, err := c.Conn.Write(buf)
		return len(b), err
	}
	if c.rawTransRecv {
		err := writeAll(c.Conn, c.buf)
		c.buf = nil
		c.rawTransSent = true
		return len(b), err
	}
	return len(b), nil
}

func writeAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return net.ErrClosed
		}
		data = data[n:]
	}
	return nil
}
