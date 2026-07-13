package protocol

import (
	"encoding/binary"
	"testing"

	"github.com/sagernet/sing-box/transport/clashssr/internal/tools"
)

func TestAuthChainRejectsOversizedUDPPadding(t *testing.T) {
	codec := newAuthChainA(&Base{Key: []byte("test-protocol-key")}).(*authChainA)
	for seed := 0; seed < 1<<16; seed++ {
		packet := make([]byte, 9)
		binary.LittleEndian.PutUint16(packet[1:3], uint16(seed))
		mac := tools.HmacMD5(codec.userKey, packet[:len(packet)-1])
		packet[len(packet)-1] = mac[0]
		md5Data := tools.HmacMD5(codec.Key, packet[len(packet)-8:len(packet)-1])
		var random tools.XorShift128Plus
		padding := udpGetRandLength(md5Data, &random)
		if padding <= len(packet)-8 {
			continue
		}
		if _, err := codec.DecodePacket(packet); err != errAuthChainLengthError {
			t.Fatalf("oversized UDP padding error = %v", err)
		}
		return
	}
	t.Fatal("failed to construct oversized UDP padding case")
}
