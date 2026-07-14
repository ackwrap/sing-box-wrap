package shadowsocksr

import (
	"net"
	"testing"

	"github.com/sagernet/sing-box/transport/clashssr/protocol"
	"github.com/sagernet/sing-box/transport/clashssr/shadowstream"
)

func TestSSRUDPPacketRoundTrip(t *testing.T) {
	cipher, err := shadowstream.New("aes-256-cfb", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	clientProtocol, err := protocol.PickProtocol("origin", &protocol.Base{Key: cipher.Key()})
	if err != nil {
		t.Fatal(err)
	}
	serverProtocol, err := protocol.PickProtocol("origin", &protocol.Base{Key: cipher.Key()})
	if err != nil {
		t.Fatal(err)
	}
	left, right := connectedPacketPair(t)
	defer left.Close()
	defer right.Close()
	client := &ssrPacketConn{PacketConn: clientProtocol.PacketConn(cipher.PacketConn(left)), serverAddr: right.LocalAddr()}
	server := &ssrPacketConn{PacketConn: serverProtocol.PacketConn(cipher.PacketConn(right)), serverAddr: left.LocalAddr()}
	payload := []byte("ssr-udp-payload")
	destination := &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 53}
	if _, err := client.WriteTo(payload, destination); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	n, address, err := server.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != string(payload) {
		t.Fatalf("packet payload = %q", buffer[:n])
	}
	if address.String() != destination.String() {
		t.Fatalf("packet destination = %s", address)
	}
}

func TestNoneCipherDerivesProtocolKey(t *testing.T) {
	cipher, err := shadowstream.New("none", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if len(cipher.Key()) != 16 || cipher.IVSize() != 0 {
		t.Fatalf("none cipher key length = %d, IV size = %d", len(cipher.Key()), cipher.IVSize())
	}
}

func connectedPacketPair(t *testing.T) (*net.UDPConn, *net.UDPConn) {
	t.Helper()
	left, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	right, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		left.Close()
		t.Fatal(err)
	}
	return left, right
}
