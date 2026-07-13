package obfs

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestHTTPObfsReadsSegmentedResponseHeader(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := (&httpObfs{Base: &Base{}}).StreamConn(client)
	go func() {
		_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n"))
		_, _ = server.Write([]byte("\r\npayload"))
	}()
	buffer := make([]byte, 32)
	n, err := conn.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "payload" {
		t.Fatalf("payload = %q", buffer[:n])
	}
}

func TestRandomHeadPropagatesReadError(t *testing.T) {
	client, server := net.Pipe()
	conn := (&randomHead{Base: &Base{}}).StreamConn(client)
	server.Close()
	defer client.Close()
	if _, err := conn.Read(make([]byte, 32)); err == nil || err != io.EOF {
		t.Fatalf("read error = %v", err)
	}
}

func TestTLS12TicketReadsSegmentedHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	codec := newTLS12Ticket(&Base{Key: []byte("test-key")}).(*tls12Ticket)
	conn := codec.StreamConn(client)
	frame := make([]byte, 76)
	frame[0], frame[1], frame[2] = 0x16, 3, 3
	binary.BigEndian.PutUint16(frame[3:5], uint16(len(frame)-5))
	copy(frame[33:43], codec.hmacSHA1(frame[11:33])[:10])
	copy(frame[len(frame)-10:], codec.hmacSHA1(frame[:len(frame)-10])[:10])
	go func() {
		clientHello := make([]byte, 2048)
		_, _ = server.Read(clientHello)
		_, _ = server.Write(frame[:20])
		_, _ = server.Write(frame[20:])
		finish := make([]byte, 2048)
		_, _ = server.Read(finish)
		_, _ = server.Write([]byte{0x17, 3, 3, 0, 7})
		_, _ = server.Write([]byte("payload"))
	}()
	if _, err := conn.Write([]byte("initial")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 32)
	n, err := conn.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "payload" {
		t.Fatalf("payload = %q", buffer[:n])
	}
}
