package continuum

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ayush/slate/internal/chaos"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/httpapi"
	"github.com/ayush/slate/internal/store"
)

type LocalDemoConfig struct {
	FrontendAddr string
	BackendAAddr string
	BackendBAddr string
	ProbeMillis  int
	AdminToken   string
}

type LocalDemo struct {
	Controller *Controller
	Control    *MemoryControl
	Store      *store.MemoryStore
	Router     *Router
	PublicURL  string

	mu       sync.Mutex
	backends map[string]*localBackend
	public   *http.Server
	publicLn net.Listener
	ctx      context.Context
	stop     context.CancelFunc
	done     chan struct{}
}

type localBackend struct {
	id       string
	addr     string
	api      *httpapi.Server
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	crashed  bool
}

func StartLocalDemo(parent context.Context, settings config.Settings, options LocalDemoConfig) (*LocalDemo, error) {
	if parent == nil {
		parent = context.Background()
	}
	if options.FrontendAddr == "" {
		options.FrontendAddr = "127.0.0.1:8080"
	}
	if options.BackendAAddr == "" {
		options.BackendAAddr = "127.0.0.1:8001"
	}
	if options.BackendBAddr == "" {
		options.BackendBAddr = "127.0.0.1:8002"
	}
	if options.AdminToken == "" {
		options.AdminToken = settings.AdminToken
	}
	probeMillis := options.ProbeMillis
	if probeMillis <= 0 {
		probeMillis = 5000
	}
	control, err := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1})
	if err != nil {
		return nil, err
	}
	data := store.NewMemory(settings.Region)
	fenced, ok := any(data).(interface{ SetFenceGuard(clusterstate.FenceGuard) })
	if !ok {
		return nil, errors.New("memory store does not support epoch fencing")
	}
	fenced.SetFenceGuard(control)
	seedAPI := httpapi.New(settings, data)
	if err := seedAPI.Seed(parent); err != nil {
		return nil, fmt.Errorf("seed local exam: %w", err)
	}
	apiASettings := settings
	apiASettings.BackendID = "A"
	apiASettings.AppRole = "active"
	apiASettings.FencingEnabled = true
	apiBSettings := settings
	apiBSettings.BackendID = "B"
	apiBSettings.AppRole = "active"
	apiBSettings.FencingEnabled = true
	apis := map[string]*httpapi.Server{"A": httpapi.New(apiASettings, data), "B": httpapi.New(apiBSettings, data)}
	backends := map[string]*localBackend{
		"A": {id: "A", addr: options.BackendAAddr, api: apis["A"]},
		"B": {id: "B", addr: options.BackendBAddr, api: apis["B"]},
	}
	for _, id := range []string{"A", "B"} {
		if err := backends[id].start(); err != nil {
			for _, started := range backends {
				_ = started.close(context.Background())
			}
			return nil, fmt.Errorf("start backend %s: %w", id, err)
		}
	}
	urls := map[string]string{
		"A": "http://" + backends["A"].address(),
		"B": "http://" + backends["B"].address(),
	}
	router := &Router{Control: control, BackendURLs: urls, Timeout: 8 * time.Second,
		MaxRequestBytes: DefaultMaxRequestBytes, MaxResponseBytes: DefaultMaxResponseBytes}
	probe := HTTPBackendProbe(nil, urls, 2*time.Second)
	faults := &localFaults{backends: backends}
	controller := NewController(control, "local", settings.Region,
		Policy{Version: PolicyVersion, ConfirmationProbes: 3, ProbeIntervalMillis: probeMillis},
		probe, faults, func(ctx context.Context) (bool, error) {
			_, err := data.ReconcileOutbox(ctx)
			return err == nil, err
		})
	for _, id := range []string{"A", "B"} {
		controller.backendStatus[id] = BackendStatus{ID: id, Reason: "starting health probe"}
	}
	publicHandler := controller.LocalHandler(router, options.AdminToken)
	public, publicLn, err := listenHTTP(options.FrontendAddr, publicHandler)
	if err != nil {
		for _, started := range backends {
			_ = started.close(context.Background())
		}
		return nil, fmt.Errorf("start local router: %w", err)
	}
	publicURL := "http://" + publicLn.Addr().String()
	controller.Smoke = func(ctx context.Context) bool {
		var report struct {
			Healthy bool `json:"healthy"`
		}
		status, err := probeHTTP(ctx, &http.Client{Timeout: 3 * time.Second}, 3*time.Second, publicURL+"/health/deep", &report)
		return err == nil && status == http.StatusOK && report.Healthy
	}
	ctx, stop := context.WithCancel(parent)
	local := &LocalDemo{Controller: controller, Control: control, Store: data, Router: router,
		PublicURL: publicURL, backends: backends, public: public, publicLn: publicLn,
		ctx: ctx, stop: stop, done: make(chan struct{})}
	go func() {
		defer close(local.done)
		if err := controller.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("continuum local monitor stopped: %v", err)
		}
	}()
	return local, nil
}

func (d *LocalDemo) Close(ctx context.Context) error {
	var errs []error
	if d.stop != nil {
		d.stop()
	}
	if d.done != nil {
		select {
		case <-d.done:
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
		}
	}
	if d.public != nil {
		if err := d.public.Shutdown(ctx); err != nil {
			_ = d.public.Close()
			errs = append(errs, err)
		}
	}
	for _, backend := range d.backends {
		if err := backend.close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *localBackend) address() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listener != nil {
		return b.listener.Addr().String()
	}
	return b.addr
}

func (b *localBackend) start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.crashed && b.server != nil {
		return nil
	}
	listener, err := net.Listen("tcp", b.addr)
	if err != nil {
		return err
	}
	b.addr = listener.Addr().String()
	apiHandler := b.api.Handler()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHandler.ServeHTTP(w, r)
	})
	b.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	b.listener = listener
	b.crashed = false
	go func(server *http.Server, ln net.Listener) {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("backend %s stopped: %v", b.id, err)
		}
	}(b.server, b.listener)
	return nil
}

func (b *localBackend) crash(_ context.Context) error {
	b.mu.Lock()
	server := b.server
	alreadyCrashed := b.crashed
	b.crashed = true
	b.mu.Unlock()
	if server != nil && !alreadyCrashed {
		return server.Close()
	}
	return nil
}

func (b *localBackend) close(ctx context.Context) error {
	b.mu.Lock()
	server := b.server
	b.crashed = true
	b.mu.Unlock()
	if server == nil {
		return nil
	}
	shutdownCtx := ctx
	if _, ok := shutdownCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		shutdownCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}
	err := server.Shutdown(shutdownCtx)
	if err != nil {
		_ = server.Close()
	}
	return err
}

func (d *LocalDemo) BackendURLs() map[string]string {
	return map[string]string{"A": "http://" + d.backends["A"].address(), "B": "http://" + d.backends["B"].address()}
}

type localFaults struct {
	backends map[string]*localBackend
}

func (f *localFaults) Inject(ctx context.Context, action, backend string) error {
	if !validBackend(backend) {
		return ErrInvalidBackend
	}
	selected := f.backends[backend]
	switch action {
	case "crash":
		return selected.crash(ctx)
	case "partial":
		value := true
		selected.api.Chaos.Configure(chaos.Patch{DBStall: &value})
	case "blip":
		value := 100
		expires := time.Now().Add(1200 * time.Millisecond).UnixMilli()
		selected.api.Chaos.Configure(chaos.Patch{ErrorPct: &value, ErrorPctExpiresMS: &expires})
	case "unsafe_standby":
		value := true
		selected.api.Chaos.Configure(chaos.Patch{DBStall: &value})
	case "lost_response":
		value := true
		selected.api.Chaos.Configure(chaos.Patch{DropResponseOnce: &value})
	default:
		return fmt.Errorf("unsupported local fault %q", action)
	}
	return nil
}

func (f *localFaults) Reset(context.Context) error {
	for _, backend := range f.backends {
		backend.api.Chaos.Reset()
		backend.mu.Lock()
		crashed := backend.crashed
		backend.mu.Unlock()
		if crashed {
			if err := backend.start(); err != nil {
				return fmt.Errorf("restart backend %s: %w", backend.id, err)
			}
		}
	}
	return nil
}

func listenHTTP(address string, handler http.Handler) (*http.Server, net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("local public router stopped: %v", err)
		}
	}()
	return server, listener, nil
}

func (b *localBackend) String() string { return fmt.Sprintf("backend-%s", b.id) }
