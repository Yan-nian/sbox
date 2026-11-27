package cluster

import (
	"context"
	"net"
	"testing"
)

func TestCheckLocalPortAndProbeExternalTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	ctx := context.Background()

	// 1. Open TCP port: CheckLocalPort -> listening=true; ProbeExternalPort -> reachable=true
	localRes, err := CheckLocalPort(ctx, "tcp", port)
	if err != nil || !localRes.Listening || localRes.Protocol != "tcp" {
		t.Fatalf("expected open local tcp port %d, got %+v err=%v", port, localRes, err)
	}

	extRes, err := ProbeExternalPort(ctx, ExternalPortProbeRequest{
		Host:      "127.0.0.1",
		Port:      port,
		Network:   "tcp",
		TimeoutMS: 1500,
	})
	if err != nil || !extRes.Reachable || extRes.LatencyMS < 1 || extRes.Protocol != "tcp" {
		t.Fatalf("expected reachable tcp probe, got %+v err=%v", extRes, err)
	}

	// 2. Closed TCP port: after closing listener, both local and external probes report closed!
	_ = ln.Close()
	localClosed, err := CheckLocalPort(ctx, "tcp", port)
	if err != nil || localClosed.Listening {
		t.Fatalf("expected closed local tcp port %d, got %+v err=%v", port, localClosed, err)
	}
	extClosed, err := ProbeExternalPort(ctx, ExternalPortProbeRequest{
		Host:      "127.0.0.1",
		Port:      port,
		Network:   "tcp",
		TimeoutMS: 800,
	})
	if err != nil || extClosed.Reachable {
		t.Fatalf("expected unreachable closed tcp port %d, got %+v err=%v", port, extClosed, err)
	}
}

func TestProbeExternalPort_QUICUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	// Mock QUIC server: verifies it receives a 1200-byte RFC 9000 Long Header packet
	// and replies with a Version Negotiation packet.
	receivedLen := make(chan int, 1)
	go func() {
		buf := make([]byte, 2048)
		n, peer, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		receivedLen <- n
		// Send mock QUIC Version Negotiation response (Long Header + version 0x00000000)
		resp := []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x08, 1, 2, 3, 4, 5, 6, 7, 8, 0x00, 0x00, 0x00, 0x01}
		_, _ = pc.WriteTo(resp, peer)
	}()

	res, err := ProbeExternalPort(context.Background(), ExternalPortProbeRequest{
		Host:      "127.0.0.1",
		Port:      port,
		Network:   "udp_quic",
		TimeoutMS: 2000,
	})
	if err != nil || !res.Reachable || res.Protocol != "udp_quic" || res.LatencyMS < 1 {
		t.Fatalf("expected reachable udp_quic probe, got %+v err=%v", res, err)
	}
	if n := <-receivedLen; n != 1200 {
		t.Fatalf("expected 1200-byte RFC 9000 QUIC probe packet, got %d bytes", n)
	}
}
