package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func localReviewPort(t *testing.T, host string) int {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("loopback %s unavailable: %v", host, err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func reviewEchoTCP(t *testing.T, host string) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("TCP loopback %s unavailable: %v", host, err)
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	return l.Addr().String(), func() { _ = l.Close() }
}

func reviewEchoUDP(t *testing.T, host string) (string, func()) {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("UDP loopback %s unavailable: %v", host, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Skipf("UDP loopback %s unavailable: %v", host, err)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], peer)
		}
	}()
	return conn.LocalAddr().String(), func() { _ = conn.Close() }
}

func reviewSOCKSRequest(t *testing.T, proxy, destination string, command byte) (net.Conn, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxy, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err = io.ReadFull(conn, method); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(method, []byte{5, 0}) {
		t.Fatalf("SOCKS method: %v", method)
	}
	request := append([]byte{5, command, 0}, reviewSOCKSAddress(t, destination)...)
	if _, err = conn.Write(request); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(conn, header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 5 || header[1] != 0 {
		t.Fatalf("SOCKS response: %v", header)
	}
	address := reviewReadSOCKSAddress(t, conn, header[3])
	return conn, address
}

func reviewSOCKSAddress(t *testing.T, address string) []byte {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		t.Fatalf("test address must be numeric: %q", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	if v4 := ip.To4(); v4 != nil {
		out = append([]byte{1}, v4...)
	} else {
		out = append([]byte{4}, ip.To16()...)
	}
	return binary.BigEndian.AppendUint16(out, uint16(port))
}

func reviewReadSOCKSAddress(t *testing.T, reader io.Reader, atyp byte) string {
	t.Helper()
	length := 0
	switch atyp {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		b := []byte{0}
		if _, err := io.ReadFull(reader, b); err != nil {
			t.Fatal(err)
		}
		length = int(b[0])
	default:
		t.Fatalf("unknown SOCKS address type %d", atyp)
	}
	b := make([]byte, length+2)
	if _, err := io.ReadFull(reader, b); err != nil {
		t.Fatal(err)
	}
	host := string(b[:length])
	if atyp != 3 {
		host = net.IP(b[:length]).String()
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(b[length:]))))
}

func reviewRoundTrip(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("echo %q, want %q", got, payload)
	}
}

func reviewFindSnapshot(t *testing.T, tracker *SessionTracker, network, destination string, closed bool, minimum int64) ReviewSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, dropped := tracker.DrainReview()
		if dropped != 0 {
			t.Fatalf("review snapshots dropped: %d", dropped)
		}
		for _, row := range rows {
			if row.Network == network && row.Destination == destination && row.Closed == closed && row.Upload >= minimum && row.Download >= minimum {
				return row
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s destination %s closed=%t", network, destination, closed)
	return ReviewSnapshot{}
}

func TestTrafficReviewLocalSOCKSEcho(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			proxyPort := localReviewPort(t, host)
			tcpEcho, closeTCP := reviewEchoTCP(t, host)
			defer closeTCP()
			udpEcho, closeUDP := reviewEchoUDP(t, host)
			defer closeUDP()
			config := fmt.Sprintf(`{"log":{"level":"error"},"inbounds":[{"type":"mixed","tag":"review-in","listen":%q,"listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, host, proxyPort)
			core := NewCore()
			if err := core.Start([]byte(config)); err != nil {
				skipIfFeatureMissing(t, err)
				t.Fatal(err)
			}
			defer func() {
				if err := core.Stop(); err != nil {
					t.Error(err)
				}
			}()
			tracker := core.GetInstance().SessionTracker()
			proxy := net.JoinHostPort(host, strconv.Itoa(proxyPort))

			// A long TCP connection is sampled twice, then its final counters are
			// recovered from the close hook; a short one closes before any sample.
			long, _ := reviewSOCKSRequest(t, proxy, tcpEcho, 1)
			reviewRoundTrip(t, long, "abc")
			first := reviewFindSnapshot(t, tracker, "tcp", tcpEcho, false, 3)
			reviewRoundTrip(t, long, "defg")
			second := reviewFindSnapshot(t, tracker, "tcp", tcpEcho, false, 7)
			if first.ID != second.ID || second.Upload != 7 || second.Download != 7 {
				t.Fatalf("long session cumulative counters: %+v %+v", first, second)
			}
			_ = long.Close()
			final := reviewFindSnapshot(t, tracker, "tcp", tcpEcho, true, 7)
			if final.ID != first.ID || final.Upload != 7 || final.Download != 7 || final.Inbound != "review-in" || final.Outbound != "direct" || final.Domain != "" {
				t.Fatalf("long session final metadata/counters: %+v", final)
			}
			sourceHost, _, err := net.SplitHostPort(final.Source)
			if err != nil || sourceHost != host || final.CreatedAt <= 0 {
				t.Fatalf("long session source metadata: %+v, err=%v", final, err)
			}
			short, _ := reviewSOCKSRequest(t, proxy, tcpEcho, 1)
			reviewRoundTrip(t, short, "xy")
			_ = short.Close()
			shortFinal := reviewFindSnapshot(t, tracker, "tcp", tcpEcho, true, 2)
			if shortFinal.ID == final.ID || shortFinal.Upload != 2 || shortFinal.Download != 2 {
				t.Fatalf("short session final: %+v", shortFinal)
			}

			// UDP ASSOCIATE uses the same local-only SOCKS inbound and echo target.
			control, relay := reviewSOCKSRequest(t, proxy, net.JoinHostPort(host, "0"), 3)
			udpPeer := relay
			relayHost, relayPort, _ := net.SplitHostPort(relay)
			if relayHost == "0.0.0.0" || relayHost == "::" {
				udpPeer = net.JoinHostPort(host, relayPort)
			}
			udpConn, err := net.DialTimeout("udp", udpPeer, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer udpConn.Close()
			_ = udpConn.SetDeadline(time.Now().Add(5 * time.Second))
			payload := []byte("udp-echo")
			packet := append(append([]byte{0, 0, 0}, reviewSOCKSAddress(t, udpEcho)...), payload...)
			if _, err := udpConn.Write(packet); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 2048)
			n, err := udpConn.Read(buffer)
			if err != nil {
				t.Fatal(err)
			}
			if n < len(payload) || !bytes.Equal(buffer[n-len(payload):n], payload) {
				t.Fatalf("UDP echo packet %v", buffer[:n])
			}
			udpLive := reviewFindSnapshot(t, tracker, "udp", udpEcho, false, int64(len(payload)))
			if udpLive.Upload != int64(len(payload)) || udpLive.Download != int64(len(payload)) {
				t.Fatalf("UDP payload counters: %+v", udpLive)
			}
			_ = control.Close()
			udpFinal := reviewFindSnapshot(t, tracker, "udp", udpEcho, true, int64(len(payload)))
			if udpFinal.ID != udpLive.ID || udpFinal.Upload != int64(len(payload)) || udpFinal.Download != int64(len(payload)) {
				t.Fatalf("UDP final counters: %+v", udpFinal)
			}
			var nativeUp, nativeDown int64
			for _, stat := range *tracker.GetStats() {
				if stat.Resource == "inbound" && stat.Tag == "review-in" {
					if stat.Direction {
						nativeUp += stat.Traffic
					} else {
						nativeDown += stat.Traffic
					}
				}
			}
			wantNative := int64(7 + 2 + len(payload))
			if nativeUp != wantNative || nativeDown != wantNative {
				t.Fatalf("DrainReview consumed original GetStats: up=%d down=%d want=%d", nativeUp, nativeDown, wantNative)
			}
		})
	}
}
