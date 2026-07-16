package clashapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"

	"github.com/go-chi/render"
)

const (
	outboundExitIPv4URL     = "https://1.1.1.1/cdn-cgi/trace"
	outboundExitIPv6URL     = "https://[2606:4700:4700::1111]/cdn-cgi/trace"
	outboundExitIPTimeout   = 12 * time.Second
	outboundExitIPBodyLimit = 64 << 10
)

func getProxyExitIP(server *Server) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !isLoopbackExitIPRequest(request.RemoteAddr) {
			render.Status(request, http.StatusForbidden)
			render.JSON(writer, request, newError("Exit IP API is only available from loopback"))
			return
		}
		query := request.URL.Query()
		if len(query) != 1 || len(query["ip_version"]) != 1 {
			render.Status(request, http.StatusBadRequest)
			render.JSON(writer, request, newError("only ip_version is supported"))
			return
		}
		var ipv6 bool
		switch query.Get("ip_version") {
		case "4":
		case "6":
			ipv6 = true
		default:
			render.Status(request, http.StatusBadRequest)
			render.JSON(writer, request, newError("ip_version must be 4 or 6"))
			return
		}

		proxy := request.Context().Value(CtxKeyProxy).(adapter.Outbound)
		ctx, cancel := context.WithTimeout(request.Context(), outboundExitIPTimeout)
		defer cancel()
		server.logger.Info("outbound exit IP check started: ", proxy.Tag())
		ip, err := lookupOutboundExitIP(ctx, proxy, ipv6)
		if err != nil {
			server.logger.Warn("outbound exit IP check failed: ", proxy.Tag())
			if isOutboundExitIPTimeout(ctx, err) {
				render.Status(request, http.StatusGatewayTimeout)
				render.JSON(writer, request, newError("Exit IP lookup timed out"))
				return
			}
			render.Status(request, http.StatusBadGateway)
			render.JSON(writer, request, newError("Unable to query exit IP through outbound"))
			return
		}
		server.logger.Info("outbound exit IP check completed: ", proxy.Tag())
		render.JSON(writer, request, render.M{
			"ip":         ip.String(),
			"ip_version": map[bool]int{false: 4, true: 6}[ipv6],
		})
	}
}

func isLoopbackExitIPRequest(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isOutboundExitIPTimeout(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func lookupOutboundExitIP(ctx context.Context, detour N.Dialer, ipv6 bool) (net.IP, error) {
	link := outboundExitIPv4URL
	if ipv6 {
		link = outboundExitIPv6URL
	}
	return fetchOutboundExitIP(ctx, link, detour, ipv6)
}

func fetchOutboundExitIP(ctx context.Context, link string, detour N.Dialer, ipv6 bool) (net.IP, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return detour.DialContext(ctx, network, M.ParseSocksaddr(address))
		},
		TLSClientConfig: &tls.Config{
			Time:    ntp.TimeFuncFromContext(ctx),
			RootCAs: adapter.RootPoolFromContext(ctx),
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   outboundExitIPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "sing-box/ackwrap")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exit IP service returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, outboundExitIPBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > outboundExitIPBodyLimit {
		return nil, errors.New("exit IP response exceeds 64 KiB")
	}
	ip, err := parseOutboundExitIP(string(body))
	if err != nil {
		return nil, err
	}
	if ipv6 == (ip.To4() != nil) {
		return nil, errors.New("exit IP service returned an unexpected address family")
	}
	return ip, nil
}

func parseOutboundExitIP(body string) (net.IP, error) {
	for _, line := range strings.Split(body, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && key == "ip" {
			if ip := net.ParseIP(strings.TrimSpace(value)); ip != nil {
				return ip, nil
			}
		}
	}
	return nil, errors.New("exit IP service returned an invalid address")
}
