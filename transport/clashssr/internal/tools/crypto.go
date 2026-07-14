package tools

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"io"
)

const HmacSHA1Len = 10

func HmacMD5(key, data []byte) []byte {
	hash := hmac.New(md5.New, key)
	hash.Write(data)
	return hash.Sum(nil)
}

func HmacSHA1(key, data []byte) []byte {
	hash := hmac.New(sha1.New, key)
	hash.Write(data)
	return hash.Sum(nil)
}

func MD5Sum(data []byte) []byte {
	sum := md5.Sum(data)
	return sum[:]
}

func SHA1Sum(data []byte) []byte {
	sum := sha1.Sum(data)
	return sum[:]
}

func AppendRandBytes(buffer *bytes.Buffer, length int) {
	_, _ = io.CopyN(buffer, rand.Reader, int64(length))
}

func KDF(password string, keyLength int) []byte {
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

type XorShift128Plus struct{ state [2]uint64 }

func (r *XorShift128Plus) Next() uint64 {
	x, y := r.state[0], r.state[1]
	r.state[0] = y
	x ^= x << 23
	x ^= y ^ (x >> 17) ^ (y >> 26)
	r.state[1] = x
	return x + y
}

func (r *XorShift128Plus) InitFromBin(data []byte) {
	full := make([]byte, 16)
	copy(full, data)
	r.state[0] = binary.LittleEndian.Uint64(full[:8])
	r.state[1] = binary.LittleEndian.Uint64(full[8:])
}

func (r *XorShift128Plus) InitFromBinAndLength(data []byte, length int) {
	full := make([]byte, 16)
	copy(full, data)
	binary.LittleEndian.PutUint16(full, uint16(length))
	r.state[0] = binary.LittleEndian.Uint64(full[:8])
	r.state[1] = binary.LittleEndian.Uint64(full[8:])
	for range 4 {
		r.Next()
	}
}
