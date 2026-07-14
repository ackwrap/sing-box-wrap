package shadowsocksr

import (
	"bytes"
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/clashssr/obfs"
	"github.com/sagernet/sing-box/transport/clashssr/protocol"
	"github.com/sagernet/sing-box/transport/clashssr/shadowstream"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.ShadowsocksROutboundOptions](registry, C.TypeShadowsocksR, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	logger     logger.ContextLogger
	dialer     N.Dialer
	serverAddr M.Socksaddr
	cipher     *shadowstream.Cipher
	obfs       obfs.Obfs
	protocol   protocol.Protocol
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.ShadowsocksROutboundOptions) (adapter.Outbound, error) {
	cipher, err := shadowstream.New(options.Method, options.Password)
	if err != nil {
		return nil, E.Cause(err, "initialize ShadowsocksR cipher")
	}
	obfuscation, obfsOverhead, err := obfs.PickObfs(options.Obfs, &obfs.Base{Host: options.Server, Port: int(options.ServerPort), Key: cipher.Key(), IVSize: cipher.IVSize(), Param: options.ObfsParam})
	if err != nil {
		return nil, E.Cause(err, "initialize ShadowsocksR obfs")
	}
	protocolCodec, err := protocol.PickProtocol(options.Protocol, &protocol.Base{Key: cipher.Key(), Overhead: obfsOverhead, Param: options.ProtocolParam})
	if err != nil {
		return nil, E.Cause(err, "initialize ShadowsocksR protocol")
	}
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	return &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeShadowsocksR, tag, options.Network.Build(), options.DialerOptions), logger: logger, dialer: outboundDialer,
		serverAddr: options.ServerOptions.Build(), cipher: cipher, obfs: obfuscation, protocol: protocolCodec,
	}, nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound, metadata.Destination = h.Tag(), destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		conn, err := h.dialer.DialContext(ctx, N.NetworkTCP, h.serverAddr)
		if err != nil {
			return nil, err
		}
		conn = h.obfs.StreamConn(conn)
		streamConn := h.cipher.StreamConn(conn)
		writeIV, err := streamConn.ObtainWriteIV()
		if err != nil {
			conn.Close()
			return nil, err
		}
		conn = h.protocol.StreamConn(streamConn, writeIV)
		if err = M.SocksaddrSerializer.WriteAddrPort(conn, destination); err != nil {
			conn.Close()
			return nil, E.Cause(err, "write ShadowsocksR request")
		}
		return conn, nil
	case N.NetworkUDP:
		packetConn, err := h.ListenPacket(ctx, destination)
		if err != nil {
			return nil, err
		}
		return bufio.NewBindPacketConn(packetConn, destination), nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound, metadata.Destination = h.Tag(), destination
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	conn, err := h.dialer.DialContext(ctx, N.NetworkUDP, h.serverAddr)
	if err != nil {
		return nil, err
	}
	packetConn := h.cipher.PacketConn(bufio.NewUnbindPacketConn(conn))
	packetConn = h.protocol.PacketConn(packetConn)
	return &ssrPacketConn{PacketConn: packetConn, serverAddr: conn.RemoteAddr()}, nil
}

type ssrPacketConn struct {
	net.PacketConn
	serverAddr net.Addr
}

func (c *ssrPacketConn) WriteTo(payload []byte, address net.Addr) (int, error) {
	var packet bytes.Buffer
	if err := M.SocksaddrSerializer.WriteAddrPort(&packet, M.SocksaddrFromNet(address)); err != nil {
		return 0, err
	}
	packet.Write(payload)
	if _, err := c.PacketConn.WriteTo(packet.Bytes(), c.serverAddr); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *ssrPacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	packet := make([]byte, len(payload)+M.MaxSocksaddrLength)
	n, _, err := c.PacketConn.ReadFrom(packet)
	if err != nil {
		return 0, nil, err
	}
	reader := bytes.NewReader(packet[:n])
	destination, err := M.SocksaddrSerializer.ReadAddrPort(reader)
	if err != nil {
		return 0, nil, E.Cause(err, "parse ShadowsocksR UDP response address")
	}
	read := copy(payload, packet[n-reader.Len():n])
	var address net.Addr = destination
	if !destination.IsFqdn() {
		address = destination.UDPAddr()
	}
	return read, address, nil
}
