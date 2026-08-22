package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// SOCKS4 dialer. Minimal CONNECT-only client.
// Protocol: https://www.openssh.com/txt/socks4.protocol
// We do NOT support SOCKS4a (remote hostname lookup) — IPv4 only.

func dialSOCKS4(ctx context.Context, network, addr, proxyAddr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	pn, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("socks4: only numeric IPv4 destinations supported, got %q", host)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("socks4: not IPv4: %q", host)
	}

	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	// Build request: VN(1) CD(1) DSTPORT(2) DSTIP(4) USERID(\0)
	req := make([]byte, 0, 9)
	req = append(req, 4, 1)
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, uint16(pn))
	req = append(req, portBuf...)
	req = append(req, ip4...)
	req = append(req, 0) // null-terminated userid
	if _, err = c.Write(req); err != nil {
		return nil, err
	}

	// Reply: VN(1) CD(1) DSTPORT(2) DSTIP(4)
	reply := make([]byte, 8)
	if _, err = readFull(c, reply); err != nil {
		return nil, err
	}
	if reply[0] != 0 {
		return nil, fmt.Errorf("socks4: bad reply VN %d", reply[0])
	}
	switch reply[1] {
	case 0x5A:
		// Granted.
		return c, nil
	case 0x5B:
		err = errors.New("socks4: rejected or failed")
	case 0x5C:
		err = errors.New("socks4: client identd failed")
	case 0x5D:
		err = errors.New("socks4: identd required")
	default:
		err = fmt.Errorf("socks4: unknown CD 0x%X", reply[1])
	}
	return nil, err
}

func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
