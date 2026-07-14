package shadowstream

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/chacha20"
)

type streamConstructor func(key, iv []byte) (cipher.Stream, error)

type Cipher struct {
	key     []byte
	ivSize  int
	encrypt streamConstructor
	decrypt streamConstructor
	dummy   bool
}

func New(method, password string) (*Cipher, error) {
	result := &Cipher{}
	var keySize int
	switch method {
	case "none", "dummy":
		if password == "" {
			return nil, fmt.Errorf("ShadowsocksR password is empty")
		}
		result.dummy = true
		result.key = kdf(password, 16)
		return result, nil
	case "aes-128-cfb":
		keySize, result.ivSize = 16, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewCFBEncrypter), blockStream(cipher.NewCFBDecrypter)
	case "aes-192-cfb":
		keySize, result.ivSize = 24, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewCFBEncrypter), blockStream(cipher.NewCFBDecrypter)
	case "aes-256-cfb":
		keySize, result.ivSize = 32, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewCFBEncrypter), blockStream(cipher.NewCFBDecrypter)
	case "aes-128-ctr":
		keySize, result.ivSize = 16, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewCTR), blockStream(cipher.NewCTR)
	case "aes-192-ctr":
		keySize, result.ivSize = 24, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewCTR), blockStream(cipher.NewCTR)
	case "aes-256-ctr":
		keySize, result.ivSize = 32, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewCTR), blockStream(cipher.NewCTR)
	case "aes-128-ofb":
		keySize, result.ivSize = 16, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewOFB), blockStream(cipher.NewOFB)
	case "aes-192-ofb":
		keySize, result.ivSize = 24, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewOFB), blockStream(cipher.NewOFB)
	case "aes-256-ofb":
		keySize, result.ivSize = 32, aes.BlockSize
		result.encrypt, result.decrypt = blockStream(cipher.NewOFB), blockStream(cipher.NewOFB)
	case "rc4-md5":
		keySize, result.ivSize = 16, 16
		result.encrypt, result.decrypt = rc4MD5Stream, rc4MD5Stream
	case "rc4-md5-6":
		keySize, result.ivSize = 16, 6
		result.encrypt, result.decrypt = rc4MD5Stream, rc4MD5Stream
	case "chacha20-ietf":
		keySize, result.ivSize = chacha20.KeySize, chacha20.NonceSize
		result.encrypt, result.decrypt = chachaStream, chachaStream
	default:
		return nil, fmt.Errorf("unsupported ShadowsocksR cipher: %s", method)
	}
	if password == "" {
		return nil, fmt.Errorf("ShadowsocksR password is empty")
	}
	result.key = kdf(password, keySize)
	return result, nil
}

func blockStream(factory func(cipher.Block, []byte) cipher.Stream) streamConstructor {
	return func(key, iv []byte) (cipher.Stream, error) {
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return factory(block, iv), nil
	}
}

func rc4MD5Stream(key, iv []byte) (cipher.Stream, error) {
	hash := md5.New()
	hash.Write(key)
	hash.Write(iv)
	return rc4.NewCipher(hash.Sum(nil))
}

func chachaStream(key, iv []byte) (cipher.Stream, error) {
	return chacha20.NewUnauthenticatedCipher(key, iv)
}

func kdf(password string, keyLength int) []byte {
	var result, previous []byte
	for len(result) < keyLength {
		hash := md5.New()
		hash.Write(previous)
		hash.Write([]byte(password))
		result = hash.Sum(result)
		previous = result[len(result)-hash.Size():]
	}
	return result[:keyLength]
}

func (c *Cipher) Key() []byte                    { return append([]byte(nil), c.key...) }
func (c *Cipher) IVSize() int                    { return c.ivSize }
func (c *Cipher) StreamConn(conn net.Conn) *Conn { return &Conn{Conn: conn, cipher: c} }
func (c *Cipher) PacketConn(conn net.PacketConn) net.PacketConn {
	return &packetConn{PacketConn: conn, cipher: c}
}

type Conn struct {
	net.Conn
	cipher      *Cipher
	readStream  cipher.Stream
	writeStream cipher.Stream
	writeIV     []byte
	writeIVSent bool
	readMutex   sync.Mutex
	writeMutex  sync.Mutex
}

func (c *Conn) ObtainWriteIV() ([]byte, error) {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	if c.cipher.dummy {
		return nil, nil
	}
	if c.writeStream == nil {
		c.writeIV = make([]byte, c.cipher.ivSize)
		if _, err := io.ReadFull(rand.Reader, c.writeIV); err != nil {
			return nil, err
		}
		stream, err := c.cipher.encrypt(c.cipher.key, c.writeIV)
		if err != nil {
			return nil, err
		}
		c.writeStream = stream
	}
	return append([]byte(nil), c.writeIV...), nil
}

func (c *Conn) Read(data []byte) (int, error) {
	c.readMutex.Lock()
	defer c.readMutex.Unlock()
	if c.cipher.dummy {
		return c.Conn.Read(data)
	}
	if c.readStream == nil {
		iv := make([]byte, c.cipher.ivSize)
		if _, err := io.ReadFull(c.Conn, iv); err != nil {
			return 0, err
		}
		stream, err := c.cipher.decrypt(c.cipher.key, iv)
		if err != nil {
			return 0, err
		}
		c.readStream = stream
	}
	n, err := c.Conn.Read(data)
	if n > 0 {
		c.readStream.XORKeyStream(data[:n], data[:n])
	}
	return n, err
}

func (c *Conn) Write(data []byte) (int, error) {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	if c.cipher.dummy {
		return c.Conn.Write(data)
	}
	if c.writeStream == nil {
		c.writeIV = make([]byte, c.cipher.ivSize)
		if _, err := io.ReadFull(rand.Reader, c.writeIV); err != nil {
			return 0, err
		}
		stream, err := c.cipher.encrypt(c.cipher.key, c.writeIV)
		if err != nil {
			return 0, err
		}
		c.writeStream = stream
	}
	packet := append([]byte(nil), data...)
	c.writeStream.XORKeyStream(packet, packet)
	if !c.writeIVSent {
		packet = append(append(make([]byte, 0, len(c.writeIV)+len(packet)), c.writeIV...), packet...)
		c.writeIVSent = true
	}
	if err := writeFull(c.Conn, packet); err != nil {
		return 0, err
	}
	return len(data), nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

type packetConn struct {
	net.PacketConn
	cipher *Cipher
}

func (c *packetConn) WriteTo(data []byte, address net.Addr) (int, error) {
	if c.cipher.dummy {
		return c.PacketConn.WriteTo(data, address)
	}
	iv := make([]byte, c.cipher.ivSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return 0, err
	}
	stream, err := c.cipher.encrypt(c.cipher.key, iv)
	if err != nil {
		return 0, err
	}
	packet := make([]byte, len(iv)+len(data))
	copy(packet, iv)
	copy(packet[len(iv):], data)
	stream.XORKeyStream(packet[len(iv):], packet[len(iv):])
	if _, err = c.PacketConn.WriteTo(packet, address); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (c *packetConn) ReadFrom(data []byte) (int, net.Addr, error) {
	if c.cipher.dummy {
		return c.PacketConn.ReadFrom(data)
	}
	packet := make([]byte, len(data)+c.cipher.ivSize)
	n, address, err := c.PacketConn.ReadFrom(packet)
	if err != nil {
		return 0, address, err
	}
	if n < c.cipher.ivSize {
		return 0, address, io.ErrUnexpectedEOF
	}
	stream, err := c.cipher.decrypt(c.cipher.key, packet[:c.cipher.ivSize])
	if err != nil {
		return 0, address, err
	}
	payload := packet[c.cipher.ivSize:n]
	stream.XORKeyStream(payload, payload)
	return copy(data, payload), address, nil
}
