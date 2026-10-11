package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/dbx"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/health"
	"github.com/herlenashavi/go-api/pkg/httpx"
)

// Settings extends config.Base with the one env var this service needs
// that isn't generic enough to belong in Base itself: which address to
// dial for its villains gRPC client. This is the standard extension
// pattern documented on config.Base's own doc comment — every
// service-specific env var gets a typed, tagged field here, not a
// hand-rolled os.Getenv helper.
type Settings struct {
	config.Base

	// VillainsAddr is where examples/villains' gRPC server listens.
	VillainsAddr string `env:"VILLAINS_GRPC_ADDR" envDefault:"localhost:9091"`
}

// loadMTLS wraps grpcx.LoadMTLSFromConfig; nil serviceRoles because this
// example only ever dials villains as a client (grpcx.Dial) and never
// runs a gRPC server of its own, so it never verifies an incoming peer
// certificate against a role map. Returns nil, nil when mTLS isn't
// configured (cfg.MTLS all empty) — entirely opt-in, so `go run .` keeps
// working with zero setup. Generate dev certs with
// `go run ./deploy/mtls/gencerts -service heroes -service villains` and
// set the three env vars it prints to turn this on; see docs/grpc.md's
// mTLS section.
func loadMTLS(cfg config.MTLSConfig) (*grpcx.MTLSStore, error) {
	return grpcx.LoadMTLSFromConfig(cfg, nil)
}

func main() {
	// SERVICE_NAME is the one required env var; default it so `go run .`
	// works with zero setup. Everything else in config.Base already has a
	// sane default (see pkg/config) — DEBUG defaults false there, but this
	// example flips it to true below so the dev-token endpoint and
	// pretty/debug logging are on out of the box; set DEBUG=false yourself
	// to see how the service behaves in its production posture.
	setDefaultEnv("SERVICE_NAME", "heroes-example")
	setDefaultEnv("DEBUG", "true")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("heroes-example exited with error", "error", err)
		os.Exit(1)
	}
}

func setDefaultEnv(key, value string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load[Settings]()
	if err != nil {
		return err
	}

	// --- Authn: dev HMAC verifier. Swap for authn.NewJWTVerifier against
	// your real IdP's JWKS endpoint in a non-example service. ---
	verifier := authn.NewHMACVerifier([]byte(cfg.DevJWTSecret), authn.ClaimsConfig{})

	// --- Authz: role hierarchy + Cedar policies from cfg.PolicyDir. ---
	roles, err := authz.NewRoleRegistry(
		authz.Role{Name: "viewer"},
		authz.Role{Name: "editor", Parents: []string{"viewer"}},
		authz.Role{Name: "admin", Parents: []string{"editor"}},
	)
	if err != nil {
		return err
	}
	policyStore, err := authz.LoadPolicyDir(cfg.PolicyDir)
	if err != nil {
		return err
	}
	authorizer := authz.New(policyStore, roles, nil)
	authorizer.Debug = cfg.Debug

	// --- Storage: in-memory with zero setup by default; set DATABASE_URL
	// to run against real Postgres instead, via PGHeroStore (store_pg.go).
	// Schema comes from examples/heroes/migrations, applied separately —
	// see examples/heroes/migrate — not by this process at boot. ---
	var store heroStore
	healthChecks := []health.Check{
		{Name: "hero-store", Checker: func(context.Context) error { return nil }},
	}

	var pool *dbx.Pool
	if cfg.DatabaseURL != "" {
		pool, err = dbx.NewPool(ctx, dbx.Config{DSN: cfg.DatabaseURL})
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		defer pool.Close()
		store = NewPGHeroStore(pool)
		healthChecks = []health.Check{
			{Name: "hero-store", Checker: pool.HealthCheck},
		}
	} else {
		store = NewMemoryHeroStore()
	}

	application, err := app.New(ctx, app.Options{
		Config:       cfg.Base,
		Verifier:     verifier,
		Authorizer:   authorizer,
		HealthChecks: healthChecks,
	})
	if err != nil {
		return err
	}

	// Custom business metrics, on top of the framework's own http.server.*
	// ones — see docs/metrics.md and metrics.go. application.Telemetry.Meter
	// only exists once app.New has returned, which is why this wrapping
	// happens here rather than alongside store's construction above.
	store, err = newInstrumentedHeroStore(application.Telemetry.Meter, store)
	if err != nil {
		return err
	}

	// Poll for policy changes so editing policies/heroes.cedar takes
	// effect without a restart — in `go run .` that means the local file;
	// under deploy/tilt it's a ConfigMap-mounted volume kubelet syncs on
	// its own schedule, which is why the poll interval here matters less
	// than kubelet's sync period. See docs/tilt-dev.md.
	go policyStore.WatchAndReload(ctx, 5*time.Second, application.Logger)

	// --- Interservice: a gRPC client to the villains service. Dialing
	// doesn't block or fail here even if villains isn't up yet (grpc.NewClient
	// connects lazily) — the first call to GetVillain is where
	// unreachability would actually surface, as a grpcx.Problem-mapped
	// error on whatever request triggered it. ---
	mtls, err := loadMTLS(cfg.MTLS)
	if err != nil {
		return err
	}
	villains, err := NewVillainsClient(cfg.VillainsAddr, grpcx.Dependencies{
		Logger:         application.Logger,
		TracerProvider: application.Telemetry.TracerProvider,
		MeterProvider:  application.Telemetry.MeterProvider,
		MTLS:           mtls,
	})
	if mtls != nil {
		go mtls.Watch(ctx, 30*time.Second, application.Logger)
		application.Logger.Info("mTLS enabled for the villains gRPC client")
	}
	if err != nil {
		return err
	}
	defer villains.Close()

	heroes := crud.NewResource(crud.Config[Hero, CreateHero, UpdateHero]{
		ResourceType: "Hero",
		Store:        store,
		Authorizer:   authorizer,
		SortableFields: map[string]bool{
			"name": true, "createdAt": true,
		},
		FilterableFields: map[string]bool{
			"status": true, "name": true,
		},
	}, cfg.Debug)

	// A hand-written route living right alongside the generated ones —
	// crud.NewResource returns a plain chi.Router, so this is just adding
	// another route to it, not a special integration point.
	heroes.Post("/{id}/retire", httpx.Handle(retireHandler(store, authorizer, cfg.Debug), cfg.Debug))
	heroes.Get("/{id}/nemesis", httpx.Handle(nemesisHandler(store, villains), cfg.Debug))

	application.Mount("/v1/heroes", heroes)

	if cfg.Debug {
		dev := chi.NewRouter()
		dev.Post("/token", httpx.Handle(devTokenHandler([]byte(cfg.DevJWTSecret), cfg.Debug), cfg.Debug, httpx.WithStatus(http.StatusCreated)))
		application.MountPublic("/dev", dev)
		application.Logger.Warn("dev-token endpoint is mounted at POST /dev/token — debug mode only, never enable in production")
	}

	return application.ListenAndServe(ctx)
}
