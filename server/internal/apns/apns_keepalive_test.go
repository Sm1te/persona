package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// APNs wants one long-lived HTTP/2 connection per provider (#4800). On http.DefaultTransport an idle connection
// closed after 90 s, so the relay (and a hub with its own key) paid TCP + TLS to Apple on most pushes.

// testCert is httptest's certificate (valid for *.example.com) and the pool that trusts it.
func testCert(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	return ts.TLS.Certificates[0], ts.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
}

// pipeListener is an in-memory listener: synctest needs connections whose blocked reads it can see (net.Pipe), which
// loopback sockets are not.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// redirect points the client's transport at a test server: a clone of the transport it really uses (the default
// one when it names none), so every connection setting under test is kept, with dial replaced and the test
// certificate trusted.
func redirect(c *Client, dial func(context.Context, string, string) (net.Conn, error), roots *x509.CertPool) *http.Transport {
	base, _ := c.http.Transport.(*http.Transport)
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	tr := base.Clone()
	tr.Proxy = nil
	tr.DialContext = dial
	tr.TLSClientConfig = &tls.Config{RootCAs: roots}
	c.http.Transport = tr
	c.Base = "https://api.example.com"
	return tr
}

func keepaliveClient(t testing.TB) *Client {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return NewWithKey(Config{KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "app.test"}, k)
}

var alert = Request{Token: "abcd", PushType: "alert", Priority: "10", Body: []byte(`{"aps":{}}`)}

// TestSendsKeepOneConnection: notifications seconds, minutes and tens of minutes apart all go over the one HTTP/2
// connection the first opened. synctest's clock moves the gaps, so the transport's own idle timer and HTTP/2 pings
// run as they would; before #4800 the push after 2 minutes opened a second connection.
func TestSendsKeepOneConnection(t *testing.T) {
	cert, roots := testCert(t)
	synctest.Test(t, func(t *testing.T) {
		ln := newPipeListener()
		var opened, h2 atomic.Int32
		srv := &http.Server{
			Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor == 2 {
					h2.Add(1)
				}
			}),
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
			ConnState: func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					opened.Add(1)
				}
			},
		}
		go func() { _ = srv.ServeTLS(ln, "", "") }()
		c := keepaliveClient(t)
		tr := redirect(c, ln.dial, roots)
		t.Cleanup(func() {
			tr.CloseIdleConnections()
			_ = srv.Close()
		})
		gaps := []time.Duration{0, 5 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
		for i, gap := range gaps {
			synctest.Sleep(gap)
			if res, err := c.Send(context.Background(), alert); err != nil || res.Status != 200 {
				t.Fatalf("push %d after %v: %+v %v", i+1, gap, res, err)
			}
			if n := opened.Load(); n != 1 {
				t.Fatalf("push %d after %v: %d connections opened so far, want 1", i+1, gap, n)
			}
		}
		if n := h2.Load(); n != int32(len(gaps)) {
			t.Fatalf("%d of %d pushes over HTTP/2", n, len(gaps))
		}
	})
}

// handshakeDelay stands in for the network round trips of a new TCP + TLS connection to APNs.
const handshakeDelay = 150 * time.Millisecond

// idleFor applies to tr what its own idle timer does over a quiet gap: connections idle longer than
// IdleConnTimeout are closed (TestSendsKeepOneConnection runs the real timer on a fake clock; a benchmark cannot
// wait minutes).
func idleFor(tr *http.Transport, gap time.Duration) {
	if tr.IdleConnTimeout > 0 && tr.IdleConnTimeout < gap {
		tr.CloseIdleConnections()
	}
}

// BenchmarkSend: one notification to a loopback HTTP/2 TLS server whose handshake takes handshakeDelay longer,
// sent right after the previous one or after 2 minutes without one.
func BenchmarkSend(b *testing.B) {
	cert, roots := testCert(b)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.EnableHTTP2 = true
	ts.Config.ErrorLog = log.New(io.Discard, "", 0) // a stray local client's failed handshake would break a result line
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		time.Sleep(handshakeDelay) // sleep-ok: simulated network latency of a new connection
		return nil, nil
	}}
	ts.StartTLS()
	defer ts.Close()
	addr := ts.Listener.Addr().String()
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	for _, gap := range []time.Duration{0, 2 * time.Minute} {
		name := "back-to-back"
		if gap > 0 {
			name = "after-" + gap.String() + "-idle"
		}
		b.Run(name, func(b *testing.B) {
			c := keepaliveClient(b)
			tr := redirect(c, dial, roots)
			defer tr.CloseIdleConnections()
			if _, err := c.Send(context.Background(), alert); err != nil { // the first connection is not what is measured
				b.Fatal(err)
			}
			for b.Loop() {
				b.StopTimer()
				idleFor(tr, gap)
				b.StartTimer()
				if res, err := c.Send(context.Background(), alert); err != nil || res.Status != 200 {
					b.Fatalf("push: %+v %v", res, err)
				}
			}
		})
	}
}
