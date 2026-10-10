// Package app bootstraps a go-api service: it wires config, logging,
// telemetry, the default middleware chain, and health endpoints into one
// chi.Router, so a service's main() is a handful of lines regardless of how
// much observability/authn/authz machinery sits behind it.
//
//	settings := config.MustLoad[Settings]()
//	application, err := app.New(ctx, app.Options{Config: settings.Base, ...})
//	application.
//		Mount("/v1/heroes", crud.NewResource(heroResourceConfig, settings.Debug)).
//		MountPublic("/v1/dummy", dummyRouter)
//	application.ListenAndServe(ctx)
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/health"
	"github.com/herlenashavi/go-api/pkg/logging"
	"github.com/herlenashavi/go-api/pkg/middleware"
	"github.com/herlenashavi/go-api/pkg/telemetry"
)

// Options configures New. Only Config is required; everything else is
// opt-in — a service with no database, no auth, and no custom health
// checks still gets a fully observable, production-shaped HTTP server.
type Options struct {
	Config config.Base

	// Verifier, if set, is required for every route mounted with Mount
	// (not MountPublic). Leave nil for a service with no authentication
	// at all (uncommon, but valid for e.g. an internal admin tool already
	// behind a trusted network boundary).
	Verifier authn.Verifier

	// Authorizer, if set, is made available to crud.Resource configs and
	// hand-written routes via App.Authorizer — App itself does not apply
	// it automatically, since *which* action/resource a route checks is
	// necessarily route-specific (see pkg/authz).
	Authorizer *authz.Authorizer

	// HealthChecks populates GET /health/readiness.
	HealthChecks []health.Check

	// CSP sets Content-Security-Policy on every response; "" omits it.
	CSP string

	// GRPCEnabled turns on a gRPC server (pkg/grpcx) alongside the HTTP
	// one, sharing the same logger, telemetry providers, Verifier, and
	// graceful-shutdown lifecycle. Register your service implementations
	// on App.GRPCServer after New returns and before calling
	// ListenAndServe. See pkg/grpcx's package doc for why authorization
	// isn't wired automatically the way authentication is.
	GRPCEnabled bool
	// GRPCHost/GRPCPort is the gRPC listen address. GRPCPort 0 means
	// OS-assigned (see App.GRPCAddr) — handy in tests, not something you'd
	// normally want in a deployed service. GRPCHost defaults to
	// Config.HTTPHost when empty.
	GRPCHost string
	GRPCPort int
	// GRPCHealthChecks populates the gRPC health service (see
	// pkg/grpcx.RegisterHealth). Defaults to HealthChecks when nil, so a
	// service with one set of dependencies doesn't need to list them
	// twice; pass a distinct slice only if gRPC and HTTP should report
	// different readiness criteria.
	GRPCHealthChecks []health.Check
	// GRPCMTLS, if set (via grpcx.LoadMTLS), turns on mutual TLS for the
	// gRPC server: it requires or accepts a client certificate per
	// TLSConfig.RequireClientCert and places the result in context as an
	// authn.ServiceIdentity. See docs/grpc.md's mTLS section. Hot
	// reload (grpcx.MTLSStore.Watch) is not started automatically — spawn
	// it yourself with whatever interval and logger you want, same as
	// authz.PolicyStore.WatchAndReload.
	GRPCMTLS *grpcx.MTLSStore
	// GRPCServerOptions are appended after go-api's own interceptors/stats
	// handler (and transport credentials, if GRPCMTLS is set) — for
	// keepalive policy, message size limits, anything pkg/grpcx doesn't
	// cover.
	GRPCServerOptions []grpc.ServerOption
}

// App is a running service's assembled dependencies plus its router.
type App struct {
	Router     chi.Router
	Config     config.Base
	Logger     *slog.Logger
	Telemetry  *telemetry.Provider
	Authorizer *authz.Authorizer

	// GRPCServer is non-nil iff Options.GRPCEnabled was true. Register
	// your generated service implementations on it before calling
	// ListenAndServe:
	//
	//	villainsv1.RegisterVillainServiceServer(application.GRPCServer, villainServer)
	GRPCServer *grpc.Server

	authMiddleware func(http.Handler) http.Handler
	server         *http.Server
	grpcHost       string
	grpcPort       int

	mu       sync.Mutex
	addr     string
	grpcAddr string
}

// Addr returns the address the HTTP server is actually listening on,
// including the OS-assigned port when Config.HTTPPort was 0 (handy in
// tests). Empty until ListenAndServe has started accepting connections.
func (a *App) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

// GRPCAddr is Addr's gRPC-server equivalent. Empty if GRPCEnabled was
// false, or before ListenAndServe has started accepting connections.
func (a *App) GRPCAddr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.grpcAddr
}

// New assembles an App: logging, OTel tracing/metrics, the default
// middleware chain (request ID, panic recovery, tracing, RED metrics,
// access log, timeout, body limit, CORS, security headers), and
// /health/{liveness,readiness,metrics}.
func New(ctx context.Context, opts Options) (*App, error) {
	cfg := opts.Config

	logger := logging.New(logging.Config{
		Level:       cfg.LogLevel,
		JSON:        !cfg.Debug,
		ServiceName: cfg.ServiceName,
		Env:         cfg.Env,
	}, middleware.RequestIDLogFields, authn.LogFields, authn.ServiceLogFields, telemetry.TraceLogFields)

	tp, err := telemetry.New(ctx, telemetry.Config{
		ServiceName:    cfg.ServiceName,
		Env:            cfg.Env,
		TracingEnabled: cfg.TracingEnabled,
		SampleRatio:    cfg.TraceSampleRate,
		MetricsEnabled: cfg.MetricsEnabled,
		OTLPEndpoint:   cfg.OTLPEndpoint,
		OTLPInsecure:   cfg.OTLPInsecure,
	})
	if err != nil {
		return nil, fmt.Errorf("app: telemetry setup: %w", err)
	}

	httpMetrics, err := telemetry.NewHTTPMetrics(tp.Meter)
	if err != nil {
		return nil, fmt.Errorf("app: http metrics: %w", err)
	}

	healthIgnore := []string{"/health"}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover(logger, cfg.Debug))
	r.Use(middleware.Tracing(tp.Tracer, tp.Propagator))
	r.Use(middleware.Metrics(middleware.MetricsConfig{Metrics: httpMetrics, IgnorePaths: healthIgnore}))
	r.Use(middleware.AccessLog(middleware.AccessLogConfig{
		Logger:         logger,
		IgnorePaths:    healthIgnore,
		LogRequestBody: cfg.Debug,
	}))
	r.Use(middleware.Timeout(time.Duration(cfg.RequestTimeoutSeconds)*time.Second, logger, cfg.Debug))
	r.Use(middleware.BodyLimit(cfg.MaxBodyBytes))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))
	r.Use(middleware.SecurityHeaders(opts.CSP))

	r.Mount("/health", health.Router(health.Config{
		Checks:         opts.HealthChecks,
		MetricsHandler: tp.MetricsHandler,
	}))

	a := &App{
		Router:     r,
		Config:     cfg,
		Logger:     logger,
		Telemetry:  tp,
		Authorizer: opts.Authorizer,
	}
	if opts.Verifier != nil {
		a.authMiddleware = authn.Middleware(opts.Verifier)
	}

	if opts.GRPCEnabled {
		grpcHealthChecks := opts.GRPCHealthChecks
		if grpcHealthChecks == nil {
			grpcHealthChecks = opts.HealthChecks
		}

		a.GRPCServer = grpcx.NewServer(grpcx.Dependencies{
			Logger:         logger,
			Tracer:         tp.Tracer,
			Meter:          tp.Meter,
			TracerProvider: tp.TracerProvider,
			MeterProvider:  tp.MeterProvider,
			Verifier:       opts.Verifier,
			MTLS:           opts.GRPCMTLS,
			Debug:          cfg.Debug,
		}, opts.GRPCServerOptions...)
		grpcx.RegisterHealth(a.GRPCServer, grpcHealthChecks, 2*time.Second)

		a.grpcHost = opts.GRPCHost
		if a.grpcHost == "" {
			a.grpcHost = cfg.HTTPHost
		}
		a.grpcPort = opts.GRPCPort
	}

	return a, nil
}

// Mount attaches h at prefix, requiring authentication first if Options.
// Verifier was set (via authn.Middleware). Use MountPublic for routes that
// must stay reachable without credentials (webhooks, public docs).
func (a *App) Mount(prefix string, h http.Handler) *App {
	if a.authMiddleware != nil {
		a.Router.With(a.authMiddleware).Mount(prefix, h)
	} else {
		a.Router.Mount(prefix, h)
	}
	return a
}

// MountPublic attaches h at prefix with no authentication requirement,
// regardless of whether a Verifier is configured.
func (a *App) MountPublic(prefix string, h http.Handler) *App {
	a.Router.Mount(prefix, h)
	return a
}

// Use appends a middleware to the router's top-level stack (applies to
// every route, including ones mounted later). Call it before any Mount.
func (a *App) Use(mw func(http.Handler) http.Handler) *App {
	a.Router.Use(mw)
	return a
}

// ListenAndServe serves the app's router until ctx is canceled, then drains
// in-flight requests (bounded by Config.ShutdownTimeoutSeconds) and flushes
// telemetry before returning. Run it with a context tied to SIGINT/SIGTERM:
//
//	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
//	defer stop()
//	if err := application.ListenAndServe(ctx); err != nil { ... }
func (a *App) ListenAndServe(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", a.Config.HTTPHost, a.Config.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("app: listen on %s: %w", addr, err)
	}

	a.mu.Lock()
	a.addr = ln.Addr().String()
	a.mu.Unlock()

	a.server = &http.Server{
		Handler:           a.Router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2) // HTTP and (if enabled) gRPC each report here
	go func() { errCh <- httpServeErr(a.server.Serve(ln)) }()
	a.Logger.Info("listening", "addr", a.Addr())

	var grpcLn net.Listener
	if a.GRPCServer != nil {
		grpcAddr := fmt.Sprintf("%s:%d", a.grpcHost, a.grpcPort)
		grpcLn, err = net.Listen("tcp", grpcAddr)
		if err != nil {
			_ = a.server.Close()
			return fmt.Errorf("app: grpc listen on %s: %w", grpcAddr, err)
		}

		a.mu.Lock()
		a.grpcAddr = grpcLn.Addr().String()
		a.mu.Unlock()

		go func() { errCh <- a.GRPCServer.Serve(grpcLn) }()
		a.Logger.Info("grpc listening", "addr", a.GRPCAddr())
	}

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("app: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		return a.shutdown()
	}
}

// httpServeErr normalizes http.Server.Serve's expected "I was asked to
// shut down" sentinel to nil, matching how grpc.Server.Serve already
// returns nil on GracefulStop/Stop — so both server kinds can share one
// error channel and one "any non-nil value here is a real failure" check.
func httpServeErr(err error) error {
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (a *App) shutdown() error {
	a.Logger.Info("shutting down", "timeout", time.Duration(a.Config.ShutdownTimeoutSeconds)*time.Second)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(a.Config.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("app: graceful shutdown: %w", err)
	}

	if a.GRPCServer != nil {
		stopped := make(chan struct{})
		go func() {
			a.GRPCServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-shutdownCtx.Done():
			a.GRPCServer.Stop() // ShutdownTimeoutSeconds elapsed; stop waiting on slow clients
		}
	}

	if err := a.Telemetry.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("app: telemetry shutdown: %w", err)
	}
	return nil
}
