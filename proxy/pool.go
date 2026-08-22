package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

// Proxy is one tested, alive proxy.
type Proxy struct {
	Addr     string        // host:port
	Proto    string        // http | socks4 | socks5
	RTT      time.Duration // measured latency
	Failures atomic.Int32  // consecutive failure counter
	mu       sync.Mutex
	healthy  bool
}

// Healthy returns true if the proxy is still considered usable.
func (p *Proxy) Healthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthy
}

// MarkFail increments the failure counter; flips unhealthy at 3.
func (p *Proxy) MarkFail() {
	n := p.Failures.Add(1)
	if n >= 3 {
		p.mu.Lock()
		p.healthy = false
		p.mu.Unlock()
	}
}

// MarkOK resets the failure counter and ensures healthy.
func (p *Proxy) MarkOK() {
	p.Failures.Store(0)
	p.mu.Lock()
	p.healthy = true
	p.mu.Unlock()
}

// Transport returns an http.Transport configured to dial through this proxy.
func (p *Proxy) Transport() *http.Transport {
	t := &http.Transport{
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   4,
	}
	switch p.Proto {
	case "http", "https":
		t.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: p.Addr})
	case "socks5":
		if dialer, err := proxy.SOCKS5("tcp", p.Addr, nil, proxy.Direct); err == nil {
			t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
	case "socks4":
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialSOCKS4(ctx, network, addr, p.Addr)
		}
	}
	return t
}

// Pool is a thread-safe, round-robin collection of Proxies.
type Pool struct {
	proxies []*Proxy
	idx     atomic.Uint64
	mu      sync.RWMutex
}

// NewProxy constructs an untested Proxy (used by --no-proxy-test path).
func NewProxy(proto, addr string) *Proxy {
	return &Proxy{Addr: addr, Proto: proto, healthy: true}
}

// NewPool wraps the given proxies (caller is responsible for already-tested).
func NewPool(proxies []*Proxy) *Pool { return &Pool{proxies: proxies} }

// Len returns the current healthy proxy count.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := 0
	for _, pr := range p.proxies {
		if pr.Healthy() {
			n++
		}
	}
	return n
}

// Next returns the next healthy proxy, or nil if pool is exhausted.
func (p *Pool) Next() *Proxy {
	if p.Len() == 0 {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := uint64(len(p.proxies))
	for i := 0; i < int(n); i++ {
		idx := (p.idx.Add(1) - 1) % n
		pr := p.proxies[idx]
		if pr.Healthy() {
			return pr
		}
	}
	return nil
}

// MarkUnhealthy drops a proxy from rotation.
func (p *Pool) MarkUnhealthy(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pr := range p.proxies {
		if pr.Addr == addr {
			pr.MarkFail()
			return
		}
	}
}

// All returns a snapshot of all proxies (healthy or not).
func (p *Pool) All() []*Proxy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Proxy, len(p.proxies))
	copy(out, p.proxies)
	return out
}

// TestEndpoint is the Discord CDN asset we ping to validate a proxy. No auth
// required, real Discord infra.
const TestEndpoint = "https://discord.com/assets/app-assets/989193655938064464/989193655938064464.webp"

// FetchAndParse pulls every URL in sources and yields ip:port lines.
func FetchAndParse(ctx context.Context, sources []TaggedSource, client *http.Client) (map[string]string, error) {
	// protocol -> address
	out := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make(chan error, len(sources))

	for _, ts := range sources {
		wg.Add(1)
		go func(ts TaggedSource) {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.Source.URL, nil)
			resp, err := client.Do(req)
			if err != nil {
				return // skip failed source
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				return
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			if err != nil {
				return
			}
			scanner := bufio.NewScanner(strings.NewReader(string(body)))
			scanner.Buffer(make([]byte, 0, 64), 1024)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				mu.Lock()
				out[ts.Protocol+"|"+line] = ts.Protocol
				mu.Unlock()
			}
		}(ts)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

// TestResult is a single proxy test outcome.
type TestResult struct {
	Proxy *Proxy
	OK    bool
}

// Test validates each candidate proxy by timing a GET to TestEndpoint.
// Returns the proxies that responded under maxMs. Concurrency = workers.
func Test(ctx context.Context, candidates map[string]string, maxMs int, workers int) []*Proxy {
	type job struct {
		proto string
		addr  string
	}
	jobs := make(chan job, len(candidates))
	results := make(chan *Proxy, len(candidates))

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				if p, ok := testOne(ctx, j.proto, j.addr, maxMs); ok {
					results <- p
				}
			}
		}()
	}

	for k, proto := range candidates {
		jobs <- job{proto: proto, addr: k[strings.Index(k, "|")+1:]}
	}
	close(jobs)
	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]*Proxy, 0, len(candidates))
	for p := range results {
		out = append(out, p)
	}
	return out
}

func testOne(ctx context.Context, proto, addr string, maxMs int) (*Proxy, bool) {
	p := &Proxy{Addr: addr, Proto: proto, healthy: true}
	t := p.Transport()
	defer t.CloseIdleConnections()
	client := &http.Client{Transport: t, Timeout: time.Duration(maxMs+500) * time.Millisecond}
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, TestEndpoint, nil)
	req.Header.Set("User-Agent", "wrack")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return nil, false
	}
	rtt := time.Since(start)
	if rtt > time.Duration(maxMs)*time.Millisecond {
		return nil, false
	}
	p.RTT = rtt
	return p, true
}

// BuildFromFile parses a one-proxy-per-line file of "ip:port" with the given
// protocol. Used by --proxy-file.
func BuildFromFile(content string, proto string) map[string]string {
	out := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64), 1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Accept "ip:port" or "scheme://ip:port"
		if u, err := url.Parse(line); err == nil && u.Host != "" {
			line = u.Host
		}
		if _, _, err := net.SplitHostPort(line); err == nil {
			out[proto+"|"+line] = proto
		}
	}
	return out
}

// Summary renders a one-line human status string for the pool.
func Summary(p *Pool, total int) string {
	if p == nil {
		return fmt.Sprintf("proxies: 0/%d live (disabled)", total)
	}
	var sum time.Duration
	live := p.All()
	for _, pr := range live {
		sum += pr.RTT
	}
	avg := time.Duration(0)
	if n := len(live); n > 0 {
		avg = sum / time.Duration(n)
	}
	return fmt.Sprintf("proxies: %d/%d live (avg %s)", len(live), total, avg)
}
