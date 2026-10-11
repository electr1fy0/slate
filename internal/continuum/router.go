package continuum

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

const (
	DefaultMaxRequestBytes  = 1 << 20
	DefaultMaxResponseBytes = 4 << 20
	DefaultRouterTimeout    = 8 * time.Second
)

// StateReader is the small subset needed by the router.
type StateReader interface {
	Get(context.Context) (ControlState, error)
}

// Router reads the active pointer on each request and forwards only to that
// backend. It never silently tries the other backend when the selected one
// fails; a monitor must verify and explicitly promote it.
type Router struct {
	Control          StateReader
	BackendURLs      map[string]string
	Client           *http.Client
	Timeout          time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	state, err := r.Control.Get(req.Context())
	if err != nil {
		http.Error(w, "continuum routing state unavailable", http.StatusServiceUnavailable)
		return
	}
	if state.Paused {
		http.Error(w, "continuum service is paused", http.StatusServiceUnavailable)
		return
	}
	baseRaw := strings.TrimSpace(r.BackendURLs[state.ActiveBackend])
	base, err := url.Parse(baseRaw)
	if err != nil || base.Scheme == "" || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		http.Error(w, "selected backend is not configured", http.StatusServiceUnavailable)
		return
	}
	if isWebSocketUpgrade(req) {
		r.serveWebSocket(w, req, base, state)
		return
	}
	maxRequest := r.MaxRequestBytes
	if maxRequest <= 0 {
		maxRequest = DefaultMaxRequestBytes
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxRequest+1))
	if err != nil {
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxRequest {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	upstreamURL := *base
	upstreamURL.Path = joinBasePath(base.Path, req.URL.Path)
	upstreamURL.RawPath = ""
	if escaped := req.URL.EscapedPath(); escaped != req.URL.Path {
		upstreamURL.RawPath = joinBasePath(base.EscapedPath(), escaped)
	}
	upstreamURL.RawQuery = req.URL.RawQuery
	upstreamURL.Fragment = ""

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultRouterTimeout
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, req.Method, upstreamURL.String(), strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return
	}
	upstreamReq.Header = cloneHeaders(req.Header)
	stripHopHeaders(upstreamReq.Header)
	upstreamReq.Header.Del(clusterstate.BackendHeader)
	upstreamReq.Header.Del(clusterstate.EpochHeader)
	upstreamReq.Header.Set(clusterstate.BackendHeader, state.ActiveBackend)
	upstreamReq.Header.Set(clusterstate.EpochHeader, strconv.FormatInt(state.Epoch, 10))
	upstreamReq.Host = base.Host

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			http.Error(w, "selected backend timed out", http.StatusGatewayTimeout)
		} else {
			http.Error(w, "selected backend unavailable", http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()
	maxResponse := r.MaxResponseBytes
	if maxResponse <= 0 {
		maxResponse = DefaultMaxResponseBytes
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		http.Error(w, "could not read selected backend response", http.StatusBadGateway)
		return
	}
	if int64(len(responseBody)) > maxResponse {
		http.Error(w, "selected backend response too large", http.StatusBadGateway)
		return
	}
	copyHeaders(w.Header(), resp.Header)
	stripHopHeaders(w.Header())
	// The response values always come from the control record, even when an
	// upstream application attempts to set these headers itself.
	w.Header().Del(clusterstate.BackendHeader)
	w.Header().Del(clusterstate.EpochHeader)
	w.Header().Set(clusterstate.BackendHeader, state.ActiveBackend)
	w.Header().Set(clusterstate.EpochHeader, strconv.FormatInt(state.Epoch, 10))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(responseBody)
}

// serveWebSocket uses ReverseProxy's supported HTTP/1.1 upgrade handling so
// the local demo's board socket stays on the same control-plane-selected
// backend as its ordinary API requests. The proxy bounds connect, TLS, and
// handshake time but leaves an established socket open until either side
// closes it.
func (r *Router) serveWebSocket(w http.ResponseWriter, req *http.Request, base *url.URL, state ControlState) {
	if req.Method != http.MethodGet || (req.Body != nil && req.Body != http.NoBody && req.ContentLength != 0) {
		http.Error(w, "invalid websocket handshake", http.StatusBadRequest)
		return
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultRouterTimeout
	}
	transport := websocketTransport(r.Client, timeout)
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(proxyReq *httputil.ProxyRequest) {
			proxyReq.Out.URL.Scheme = base.Scheme
			proxyReq.Out.URL.Host = base.Host
			proxyReq.Out.URL.Path = joinBasePath(base.Path, proxyReq.In.URL.Path)
			proxyReq.Out.URL.RawPath = ""
			if escaped := proxyReq.In.URL.EscapedPath(); escaped != proxyReq.In.URL.Path {
				proxyReq.Out.URL.RawPath = joinBasePath(base.EscapedPath(), escaped)
			}
			proxyReq.Out.URL.RawQuery = proxyReq.In.URL.RawQuery
			proxyReq.Out.URL.Fragment = ""
			proxyReq.Out.Host = base.Host
			proxyReq.Out.Header.Del(clusterstate.BackendHeader)
			proxyReq.Out.Header.Del(clusterstate.EpochHeader)
			proxyReq.Out.Header.Set(clusterstate.BackendHeader, state.ActiveBackend)
			proxyReq.Out.Header.Set(clusterstate.EpochHeader, strconv.FormatInt(state.Epoch, 10))
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del(clusterstate.BackendHeader)
			response.Header.Del(clusterstate.EpochHeader)
			response.Header.Set(clusterstate.BackendHeader, state.ActiveBackend)
			response.Header.Set(clusterstate.EpochHeader, strconv.FormatInt(state.Epoch, 10))
			return nil
		},
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, err error) {
			response.Header().Set(clusterstate.BackendHeader, state.ActiveBackend)
			response.Header().Set(clusterstate.EpochHeader, strconv.FormatInt(state.Epoch, 10))
			if errors.Is(err, context.DeadlineExceeded) {
				http.Error(response, "selected backend timed out", http.StatusGatewayTimeout)
				return
			}
			http.Error(response, "selected backend unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, req)
}

func websocketTransport(client *http.Client, timeout time.Duration) http.RoundTripper {
	var transport *http.Transport
	if client != nil {
		transport, _ = client.Transport.(*http.Transport)
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	transport = transport.Clone()
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport.DialContext = dialer.DialContext
	transport.TLSHandshakeTimeout = timeout
	transport.ResponseHeaderTimeout = timeout
	return transport
}

func isWebSocketUpgrade(req *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(req.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, token := range strings.Split(req.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

func joinBasePath(base, requestPath string) string {
	base = strings.TrimRight(base, "/")
	if requestPath == "" || requestPath == "/" {
		if base == "" {
			return "/"
		}
		return base + "/"
	}
	return base + "/" + strings.TrimLeft(requestPath, "/")
}

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"}

func stripHopHeaders(header http.Header) {
	for _, name := range hopHeaders {
		if values := header.Values("Connection"); len(values) > 0 {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					header.Del(strings.TrimSpace(token))
				}
			}
		}
		header.Del(name)
	}
}

// EncodeBody decodes an API Gateway v2 request's optional base64 body.
func EncodeBody(body string, isBase64 bool, limit int64) ([]byte, error) {
	if !isBase64 {
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("request body exceeds %d bytes", limit)
		}
		return []byte(body), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("decode base64 request body: %w", err)
	}
	if int64(len(decoded)) > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	return decoded, nil
}
