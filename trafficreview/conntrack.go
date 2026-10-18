package trafficreview

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FlowTuple is a kernel conntrack tuple. Original and reply tuples must stay
// separate: NAT can change addresses, and neither tuple alone identifies a
// billable VPS-to-website direction.
type FlowTuple struct {
	SourceIP, DestinationIP     string
	SourcePort, DestinationPort uint16
}

// ConntrackRecord reports absolute kernel counters, never a byte delta.
// Event is new, snapshot, or destroy. A missing counter is different from zero.
type ConntrackRecord struct {
	At                              int64
	Event, Protocol, RawID          string
	Original, Reply                 FlowTuple
	OrigBytes, ReplyBytes           uint64
	OrigBytesKnown, ReplyBytesKnown bool
}

// ConntrackHealth exposes collection gaps. EventGaps counts interrupted event
// streams; snapshots cannot recover flows that began and ended inside a gap.
type ConntrackHealth struct {
	Available      bool   `json:"available"`
	Degraded       bool   `json:"degraded"`
	EventListeners int    `json:"eventListeners"`
	EventRestarts  uint64 `json:"eventRestarts"`
	EventGaps      uint64 `json:"eventGaps"`
	ENOBUFS        uint64 `json:"enobufs"`
	ParseErrors    uint64 `json:"parseErrors"`
	LastError      string `json:"lastError"`
}

// ConntrackCollector uses only the fixed conntrack executable and fixed flags.
// Record and health callbacks can run concurrently; callers must not block them.
type ConntrackCollector struct {
	onRecord   func(ConntrackRecord)
	onHealth   func(ConntrackHealth)
	mu         sync.Mutex
	health     ConntrackHealth
	snapshotOK bool
	eventSeen  bool
}

var errUnsupportedProtocol = errors.New("unsupported or missing TCP/UDP protocol")

func NewConntrackCollector(onRecord func(ConntrackRecord), onHealth func(ConntrackHealth)) *ConntrackCollector {
	return &ConntrackCollector{onRecord: onRecord, onHealth: onHealth}
}

// Run starts one unfiltered IPv4/IPv6 event stream and periodic per-family
// active-flow snapshots. conntrack 1.4.6 rejects -f with -E.
// Missing conntrack or insufficient privilege is reported through health and
// returns without preventing the rest of the proxy from running.
func (c *ConntrackCollector) Run(ctx context.Context, snapshotInterval time.Duration) {
	if snapshotInterval <= 0 {
		snapshotInterval = 30 * time.Second
	}
	path, err := exec.LookPath("conntrack")
	if err != nil {
		c.changeHealth(func(h *ConntrackHealth) { h.Degraded = true; h.LastError = "conntrack executable unavailable" })
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.eventLoop(ctx, path)
	}()
	go func() {
		defer wg.Done()
		c.snapshotLoop(ctx, path, snapshotInterval)
	}()
	wg.Wait()
}

func eventArgs() []string {
	return []string{"-E", "-e", "NEW,DESTROY", "-o", "timestamp,extended,id"}
}

func (c *ConntrackCollector) eventLoop(ctx context.Context, path string) {
	attempt := 0
	for ctx.Err() == nil {
		if attempt > 0 {
			c.changeHealth(func(h *ConntrackHealth) { h.EventRestarts++ })
		}
		attempt++
		cmd := exec.CommandContext(ctx, path, eventArgs()...)
		stdout, err := cmd.StdoutPipe()
		if err == nil {
			var stderr io.ReadCloser
			stderr, err = cmd.StderrPipe()
			if err == nil {
				err = cmd.Start()
				if err == nil {
					c.changeHealth(func(h *ConntrackHealth) {
						h.EventListeners++
					})
					var scanWG sync.WaitGroup
					scanWG.Add(1)
					go func() { defer scanWG.Done(); c.scanStderr(stderr, true) }()
					readErr := c.scanRecords(stdout, "")
					if readErr != nil {
						_ = cmd.Process.Kill()
					}
					scanWG.Wait()
					waitErr := cmd.Wait()
					if readErr != nil {
						err = readErr
					} else {
						err = waitErr
					}
					c.changeHealth(func(h *ConntrackHealth) {
						h.EventListeners--
						if h.EventListeners == 0 {
							c.eventSeen = false
						}
						h.Degraded = true
						h.Available = c.eventSeen || c.snapshotOK
					})
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		c.changeHealth(func(h *ConntrackHealth) {
			h.EventGaps++
			h.Degraded = true
			h.LastError = fmt.Sprintf("conntrack event: %v", err)
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *ConntrackCollector) snapshotLoop(ctx context.Context, path string, interval time.Duration) {
	c.snapshot(ctx, path)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.snapshot(ctx, path)
		}
	}
}

func (c *ConntrackCollector) snapshot(ctx context.Context, path string) {
	for _, family := range []string{"ipv4", "ipv6"} {
		if ctx.Err() != nil {
			return
		}
		cmd := exec.CommandContext(ctx, path, "-L", "-f", family, "-o", "extended,id")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			c.snapshotError(family, err)
			continue
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			c.snapshotError(family, err)
			continue
		}
		if err = cmd.Start(); err != nil {
			c.snapshotError(family, err)
			continue
		}
		var scanWG sync.WaitGroup
		scanWG.Add(1)
		go func() { defer scanWG.Done(); c.scanStderr(stderr, false) }()
		readErr := c.scanRecords(stdout, "snapshot")
		if readErr != nil {
			_ = cmd.Process.Kill()
		}
		scanWG.Wait()
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		if readErr != nil {
			c.snapshotError(family, readErr)
			continue
		}
		if waitErr != nil {
			c.snapshotError(family, waitErr)
			continue
		}
		c.changeHealth(func(h *ConntrackHealth) {
			c.snapshotOK = true
			h.Available = true
		})
	}
}

func (c *ConntrackCollector) snapshotError(family string, err error) {
	c.changeHealth(func(h *ConntrackHealth) {
		c.snapshotOK = false
		h.Available = c.eventSeen
		h.Degraded = true
		h.LastError = fmt.Sprintf("conntrack snapshot %s: %v", family, err)
	})
}

func (c *ConntrackCollector) scanRecords(r io.Reader, forcedEvent string) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		rec, err := parseConntrackLine(line, forcedEvent, time.Now().Unix())
		if err != nil {
			if errors.Is(err, errUnsupportedProtocol) {
				continue
			}
			c.changeHealth(func(h *ConntrackHealth) {
				h.ParseErrors++
				h.Degraded = true
				h.LastError = "conntrack parse: " + err.Error()
			})
			continue
		}
		if c.onRecord != nil {
			c.onRecord(rec)
		}
		if forcedEvent == "" {
			c.changeHealth(func(h *ConntrackHealth) { c.eventSeen = true; h.Available = true })
		}
	}
	return scanner.Err()
}

func (c *ConntrackCollector) scanStderr(r io.Reader, eventStream bool) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), "enobufs") || strings.Contains(strings.ToLower(line), "no buffer space available") {
			c.changeHealth(func(h *ConntrackHealth) {
				h.ENOBUFS++
				if eventStream {
					h.EventGaps++
				}
				h.Degraded = true
				h.LastError = line
			})
		} else if !strings.Contains(line, "flow entries") {
			c.changeHealth(func(h *ConntrackHealth) { h.LastError = line })
		}
	}
	if err := scanner.Err(); err != nil {
		c.changeHealth(func(h *ConntrackHealth) { h.Degraded = true; h.LastError = "conntrack stderr: " + err.Error() })
	}
}

func (c *ConntrackCollector) changeHealth(change func(*ConntrackHealth)) {
	c.mu.Lock()
	change(&c.health)
	health := c.health
	c.mu.Unlock()
	if c.onHealth != nil {
		c.onHealth(health)
	}
}

func parseConntrackLine(line, forcedEvent string, at int64) (ConntrackRecord, error) {
	fields := strings.Fields(line)
	rec := ConntrackRecord{At: at, Event: forcedEvent}
	protoAt := -1
	unsupported := false
	for i, field := range fields {
		if i == 0 && strings.HasPrefix(field, "[") && strings.HasSuffix(field, "]") {
			if eventAt, ok := conntrackTimestamp(strings.Trim(field, "[]")); ok {
				rec.At = eventAt
			}
		}
		switch strings.ToLower(strings.Trim(field, "[]")) {
		case "new", "update", "destroy":
			if forcedEvent == "" {
				rec.Event = strings.ToLower(strings.Trim(field, "[]"))
			}
		case "tcp", "udp":
			if protoAt < 0 {
				protoAt = i
				rec.Protocol = strings.ToLower(field)
			}
		case "icmp", "icmpv6", "sctp", "dccp", "gre":
			unsupported = true
		}
	}
	if protoAt < 0 {
		if unsupported {
			return rec, errUnsupportedProtocol
		}
		return rec, errors.New("missing TCP/UDP protocol")
	}
	if rec.Event == "" {
		return rec, errors.New("missing event type")
	}
	first, second := -1, -1
	for i := protoAt + 1; i < len(fields); i++ {
		if strings.HasPrefix(fields[i], "src=") {
			if first < 0 {
				first = i
			} else {
				second = i
				break
			}
		}
	}
	if first < 0 || second < 0 {
		return rec, errors.New("missing original/reply tuple")
	}
	var err error
	rec.Original, rec.OrigBytes, rec.OrigBytesKnown, err = parseTuple(fields[first:second])
	if err != nil {
		return rec, fmt.Errorf("original: %w", err)
	}
	rec.Reply, rec.ReplyBytes, rec.ReplyBytesKnown, err = parseTuple(fields[second:])
	if err != nil {
		return rec, fmt.Errorf("reply: %w", err)
	}
	for _, field := range fields {
		if strings.HasPrefix(field, "id=") {
			rec.RawID = strings.TrimPrefix(field, "id=")
		}
	}
	return rec, nil
}

func conntrackTimestamp(s string) (int64, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Unix(), true
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", s, time.Local); err == nil {
		return t.Unix(), true
	}
	seconds, _, _ := strings.Cut(s, ".")
	n, err := strconv.ParseInt(seconds, 10, 64)
	return n, err == nil
}

func parseTuple(fields []string) (FlowTuple, uint64, bool, error) {
	var tuple FlowTuple
	var bytes uint64
	var known bool
	var haveSrc, haveDst, haveSport, haveDport bool
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "src":
			if !haveSrc {
				tuple.SourceIP = value
				haveSrc = true
			}
		case "dst":
			if !haveDst {
				tuple.DestinationIP = value
				haveDst = true
			}
		case "sport":
			if !haveSport {
				n, err := strconv.ParseUint(value, 10, 16)
				if err != nil {
					return tuple, 0, false, err
				}
				tuple.SourcePort = uint16(n)
				haveSport = true
			}
		case "dport":
			if !haveDport {
				n, err := strconv.ParseUint(value, 10, 16)
				if err != nil {
					return tuple, 0, false, err
				}
				tuple.DestinationPort = uint16(n)
				haveDport = true
			}
		case "bytes":
			if !known {
				n, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return tuple, 0, false, err
				}
				bytes = n
				known = true
			}
		}
	}
	if !haveSrc || !haveDst || !haveSport || !haveDport {
		return tuple, 0, false, errors.New("incomplete tuple")
	}
	if _, err := netip.ParseAddr(tuple.SourceIP); err != nil {
		return tuple, 0, false, err
	}
	if _, err := netip.ParseAddr(tuple.DestinationIP); err != nil {
		return tuple, 0, false, err
	}
	return tuple, bytes, known, nil
}
