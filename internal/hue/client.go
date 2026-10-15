package hue

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Root certificates published by Signify; provenance is documented in ca.md.
//
//go:embed ca.pem
var rootPEM []byte

const maxRetries = 3
const maxBody = 16 << 20

type Client struct {
	base           string
	key            string
	http           *http.Client
	limiter        *rate.Limiter
	sem            chan struct{}
	readSem        chan struct{}
	reads          *readPacer
	capabilitiesMu sync.Mutex
	capabilities   map[string]Light
}
type APIError struct {
	Status       int
	Descriptions []string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Hue API error (HTTP %d): %s", e.Status, strings.Join(e.Descriptions, "; "))
}
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func ValidateHost(host string) error {
	if host == "" || strings.TrimSpace(host) != host || strings.ContainsAny(host, "/?#@\\ \t\n\r") {
		return fmt.Errorf("host must be an IP address or hostname without scheme or port")
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if strings.Contains(host, ":") || len(host) > 253 {
		return fmt.Errorf("host must not include a scheme or port")
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid hostname")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("invalid hostname")
			}
		}
	}
	return nil
}

// ParseBridgeID returns a bridge ID, 16 hexadecimal digits, in lower case.
func ParseBridgeID(id string) (string, error) {
	id = strings.ToLower(id)
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", errors.New("bridge ID must be 16 hexadecimal digits")
	}
	return id, nil
}

// TLSConfig accepts a certificate that chains to roots and whose common name,
// the bridge ID in lower case, accept allows.
func TLSConfig(roots *x509.CertPool, accept func(bridgeID string) error) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12,
		// A bridge certificate names the bridge ID, not its address, so Go's
		// hostname verification cannot be used. The callback performs the chain,
		// validity and server-auth checks, then matches the ID instead.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("bridge sent no certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			intermediates := x509.NewCertPool()
			for _, raw := range rawCerts[1:] {
				cert, err := x509.ParseCertificate(raw)
				if err != nil {
					return err
				}
				intermediates.AddCert(cert)
			}
			if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return err
			}
			return accept(strings.ToLower(leaf.Subject.CommonName))
		},
	}
}

func bridgeRoots() (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return nil, errors.New("invalid embedded CA")
	}
	return roots, nil
}

// ObserveBridgeID reads the bridge ID from the certificate that host presents.
// The certificate must chain to the Hue root CA. It is for first contact, when
// no bridge ID is known yet.
func ObserveBridgeID(ctx context.Context, host string) (string, error) {
	if err := ValidateHost(host); err != nil {
		return "", err
	}
	roots, err := bridgeRoots()
	if err != nil {
		return "", err
	}
	return observeBridgeID(ctx, net.JoinHostPort(host, "443"), roots)
}

func observeBridgeID(ctx context.Context, addr string, roots *x509.CertPool) (string, error) {
	var observed string
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: TLSConfig(roots, func(id string) error {
		observed = id
		return nil
	})}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	conn.Close()
	id, err := ParseBridgeID(observed)
	if err != nil {
		return "", fmt.Errorf("the bridge's certificate does not name a bridge ID: %w", err)
	}
	return id, nil
}

// NewClient connects only to the bridge whose certificate names bridgeID.
func NewClient(host, key, bridgeID string) (*Client, error) {
	if err := ValidateHost(host); err != nil {
		return nil, err
	}
	want, err := ParseBridgeID(bridgeID)
	if err != nil {
		return nil, err
	}
	roots, err := bridgeRoots()
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Bridge credentials must never be sent through environment proxies.
	transport.TLSClientConfig = TLSConfig(roots, func(id string) error {
		if id != want {
			return fmt.Errorf("the bridge at %s is bridge %s, not %s", host, id, want)
		}
		return nil
	})
	transport.MaxConnsPerHost = 5
	transport.MaxIdleConnsPerHost = 5
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	return NewClientWithHTTP("https://"+authority, key, &http.Client{Transport: transport, Timeout: 30 * time.Second})
}

// NewClientWithHTTP is a dependency-injection seam for local fake bridges. It is
// not exposed in the provider schema or CLI flags. HTTPS remains mandatory.
func NewClientWithHTTP(base, key string, h *http.Client) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("invalid HTTPS bridge URL")
	}
	if h == nil {
		return nil, errors.New("HTTP client is required")
	}
	copyHTTP := *h
	copyHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyHTTP.Timeout == 0 {
		copyHTTP.Timeout = 30 * time.Second
	}
	return &Client{base: base, key: key, http: &copyHTTP, limiter: rate.NewLimiter(5, 1), sem: make(chan struct{}, 1), readSem: make(chan struct{}, 4), reads: newReadPacer()}, nil
}

func validPath(p string) bool {
	u, err := url.Parse(p)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || strings.ContainsAny(p, "\\\r\n") {
		return false
	}
	if !(strings.HasPrefix(u.Path, "/clip/v2/") || p == "/api") {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (c *Client) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if !validPath(path) {
		return nil, errors.New("path must be a local /clip/v2/ endpoint")
	}
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	sem := c.sem
	if method == http.MethodGet {
		sem = c.readSem
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	for attempt := 0; attempt <= maxRetries; attempt++ {
		var epoch uint64
		if method == http.MethodGet {
			epoch, err = c.reads.acquire(ctx)
		} else {
			err = c.limiter.Wait(ctx)
		}
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if c.key != "" {
			req.Header.Set("hue-application-key", c.key)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > maxBody {
			return nil, errors.New("bridge response exceeds size limit")
		}
		if resp.StatusCode == 429 {
			delay := retryDelay(resp.Header.Get("Retry-After"), time.Now(), attempt)
			if method == http.MethodGet {
				c.reads.throttle(delay)
			}
			if attempt < maxRetries {
				if method != http.MethodGet {
					if err := wait(ctx, delay); err != nil {
						return nil, err
					}
				}
				continue
			}
		}
		var envelope struct {
			Errors []struct {
				Description string `json:"description"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(data, &envelope)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || len(envelope.Errors) > 0 {
			descriptions := []string{}
			for _, e := range envelope.Errors {
				description := e.Description
				if c.key != "" {
					description = strings.ReplaceAll(description, c.key, "[redacted]")
				}
				descriptions = append(descriptions, description)
			}
			return nil, &APIError{Status: resp.StatusCode, Descriptions: descriptions}
		}
		if method == http.MethodGet {
			c.reads.success(epoch)
		}
		return data, nil
	}
	return nil, errors.New("retry limit exceeded")
}
func retryDelay(value string, now time.Time, attempt int) time.Duration {
	if n, err := strconv.ParseInt(value, 10, 32); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		if d := date.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return time.Second * time.Duration(1<<attempt)
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Client) Raw(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/clip/v2/") {
		return nil, errors.New("raw accepts only /clip/v2/ paths")
	}
	return c.request(ctx, http.MethodGet, path, nil)
}
func (c *Client) Get(ctx context.Context, path string, out any) error {
	data, err := c.Raw(ctx, path)
	if err != nil {
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode Hue response: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("Hue response has no data array")
	}
	return json.Unmarshal(envelope.Data, out)
}
func (c *Client) Create(ctx context.Context, kind string, body any) (string, error) {
	data, err := c.request(ctx, http.MethodPost, "/clip/v2/resource/"+kind, body)
	if err != nil {
		return "", err
	}
	var response struct {
		Data []Reference `json:"data"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return "", err
	}
	if len(response.Data) != 1 || response.Data[0].RID == "" {
		return "", errors.New("create response must contain one resource reference")
	}
	return response.Data[0].RID, nil
}
func (c *Client) Update(ctx context.Context, kind, id string, body any) error {
	_, err := c.request(ctx, http.MethodPut, "/clip/v2/resource/"+kind+"/"+url.PathEscape(id), body)
	return err
}
func (c *Client) Delete(ctx context.Context, kind, id string) error {
	_, err := c.request(ctx, http.MethodDelete, "/clip/v2/resource/"+kind+"/"+url.PathEscape(id), nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
func GetOne[T any](ctx context.Context, c *Client, kind, id string) (T, error) {
	var zero T
	var items []T
	err := c.Get(ctx, "/clip/v2/resource/"+kind+"/"+url.PathEscape(id), &items)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, &APIError{Status: 404}
	}
	if len(items) != 1 {
		return zero, errors.New("expected one resource")
	}
	return items[0], nil
}

// Register is the sole v1 operation, used only by the explicit init command.
func (c *Client) Register(ctx context.Context) (string, bool, error) {
	data, err := c.request(ctx, http.MethodPost, "/api", map[string]string{"devicetype": "hue-tf#cli"})
	if err != nil {
		return "", false, err
	}
	var result []struct {
		Success *struct {
			Username string `json:"username"`
		} `json:"success"`
		Error *struct {
			Type        int    `json:"type"`
			Address     string `json:"address"`
			Description string `json:"description"`
		} `json:"error"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return "", false, err
	}
	if len(result) != 1 {
		return "", false, errors.New("unexpected registration response")
	}
	if result[0].Error != nil {
		if result[0].Error.Type == 101 {
			return "", true, nil
		}
		apiErr := result[0].Error
		message := fmt.Sprintf("registration error %d", apiErr.Type)
		if apiErr.Address != "" {
			message += " at " + apiErr.Address
		}
		if apiErr.Description != "" {
			message += ": " + apiErr.Description
		}
		if c.key != "" {
			message = strings.ReplaceAll(message, c.key, "[redacted]")
		}
		return "", false, errors.New(message)
	}
	if result[0].Success == nil || result[0].Success.Username == "" {
		return "", false, errors.New("registration returned no application key")
	}
	return result[0].Success.Username, false, nil
}
