package hue

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c, err := NewClientWithHTTP(s.URL, "secret", s.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestClientHTTP(t *testing.T) {
	var methods []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("hue-application-key") != "secret" {
			t.Error("missing key")
		}
		methods = append(methods, r.Method)
		if r.Method == "POST" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		_, _ = w.Write([]byte(`{"errors":[],"data":[{"rid":"id","rtype":"room"}]}`))
	})
	ctx := context.Background()
	var refs []Reference
	if err := c.Get(ctx, "/clip/v2/resource/room", &refs); err != nil {
		t.Fatal(err)
	}
	if id, err := c.Create(ctx, "room", map[string]string{"name": "Room"}); err != nil || id != "id" {
		t.Fatalf("%s %v", id, err)
	}
	if err := c.Update(ctx, "room", "id", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "room", "id"); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 4 {
		t.Fatal(methods)
	}
}
func TestClientRetry(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{"errors":[],"data":[]}`))
	})
	if _, err := c.Raw(context.Background(), "/clip/v2/resource/light"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal(calls.Load())
	}
	calls.Store(0)
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
	})
	if _, err := c.Raw(context.Background(), "/clip/v2/resource/light"); err == nil {
		t.Fatal("expected retry exhaustion")
	}
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
	now := time.Now().UTC().Truncate(time.Second)
	if retryDelay(now.Add(3*time.Second).Format(http.TimeFormat), now, 0) != 3*time.Second || retryDelay("2", now, 0) != 2*time.Second || retryDelay("invalid", now, 2) != 4*time.Second {
		t.Fatal("retry-after parsing")
	}
}
func TestClientCancellationAndRate(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	var active, maxActive atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		a := active.Add(1)
		defer active.Add(-1)
		if a > maxActive.Load() {
			maxActive.Store(a)
		}
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"errors":[],"data":[]}`))
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Raw(context.Background(), "/clip/v2/resource/light"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatal("concurrent requests")
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 180*time.Millisecond {
			t.Fatal("rate limit exceeded")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Raw(ctx, "/clip/v2/resource/light"); err == nil {
		t.Fatal("cancel ignored")
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	})
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Raw(ctx, "/clip/v2/resource/light"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestClientErrorsAndRedirect(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{200, `{"errors":[{"description":"secret invalid"}],"data":[]}`}, {404, `{"errors":[],"data":[]}`}, {500, `not json`}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Raw(context.Background(), "/clip/v2/resource/room")
			if err == nil {
				t.Fatal("missing error")
			}
			if tc.status == 404 && !IsNotFound(err) {
				t.Fatal(err)
			}
			if tc.status == 200 && err.Error() != "Hue API error (HTTP 200): [redacted] invalid" {
				t.Fatal(err)
			}
		})
	}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) })
	if _, err := c.Raw(context.Background(), "/clip/v2/resource/light"); err == nil {
		t.Fatal("redirect accepted")
	}
	for _, p := range []string{"https://evil.test/clip/v2/resource/light", "/api", "/clip/v2/../../api", "/clip/v2/%2e%2e/api", "//evil.test/clip/v2/light"} {
		if _, err := c.Raw(context.Background(), p); err == nil {
			t.Errorf("accepted %s", p)
		}
	}
}
func TestHostValidation(t *testing.T) {
	for _, host := range []string{"192.168.1.10", "hue.local", "2001:db8::1"} {
		if err := ValidateHost(host); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"", "https://hue.local", "hue.local:443", "hue.local/api", "user@hue.local", "hue.local?x=1", " hue.local", "-hue.local"} {
		if err := ValidateHost(host); err == nil {
			t.Errorf("accepted %q", host)
		}
	}
}
func TestTLSChainVerification(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(raw)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	for _, tc := range []struct {
		name                           string
		expired, clientAuth, untrusted bool
	}{{name: "CN mismatch allowed"}, {name: "expired", expired: true}, {name: "client usage rejected", clientAuth: true}, {name: "untrusted", untrusted: true}} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "bridge-id-not-host"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			if tc.expired {
				leaf.NotAfter = time.Now().Add(-time.Minute)
			}
			if tc.clientAuth {
				leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			}
			raw, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			pool := roots
			if tc.untrusted {
				pool = x509.NewCertPool()
			}
			err = TLSConfig(pool).VerifyPeerCertificate([][]byte{raw}, nil)
			wantErr := tc.expired || tc.clientAuth || tc.untrusted
			if (err != nil) != wantErr {
				t.Fatalf("verification: %v", err)
			}
		})
	}
	count := 0
	remaining := rootPEM
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA {
			t.Fatal("invalid embedded root")
		}
		if err := cert.CheckSignatureFrom(cert); err != nil {
			t.Fatal(err)
		}
		remaining = rest
		count++
	}
	if count != 2 {
		t.Fatalf("root count %d", count)
	}
}
func TestRegistration(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" || r.Method != "POST" {
			t.Error("registration endpoint")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 1 || payload["devicetype"] != "hue-tf#cli" {
			t.Errorf("unexpected registration payload: %#v", payload)
		}
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`[{"error":{"type":101}}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"success":{"username":"new-key"}}]`))
	})
	if _, pending, err := c.Register(context.Background()); !pending || err != nil {
		t.Fatalf("%v %v", pending, err)
	}
	if key, pending, err := c.Register(context.Background()); key != "new-key" || pending || err != nil {
		t.Fatalf("%s %v %v", key, pending, err)
	}
}

func TestRegistrationErrorDetails(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"parameter error", `[{"error":{"type":7,"address":"/generateclientkey","description":"invalid value for parameter"}}]`, "registration error 7 at /generateclientkey: invalid value for parameter"},
		{"bare code", `[{"error":{"type":7}}]`, "registration error 7"},
		{"redaction", `[{"error":{"type":7,"address":"/secret","description":"secret rejected"}}]`, "registration error 7 at /[redacted]: [redacted] rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			key, pending, err := c.Register(context.Background())
			if err == nil || err.Error() != tc.want || pending || key != "" {
				t.Fatalf("key=%q pending=%v err=%v", key, pending, err)
			}
		})
	}
}
