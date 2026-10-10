package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/health"
	"github.com/herlenashavi/go-api/pkg/httpx"
)

// devJWTSecret signs/verifies dev tokens for this example only — see
// authn.NewHMACVerifier's doc comment for why this is never the production
// path. Override with HERO_JWT_SECRET if you want a non-default value.
func devJWTSecret() []byte {
	if s := os.Getenv("HERO_JWT_SECRET"); s != "" {
		return []byte(s)
	}
	return []byte("dev-secret-change-me")
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
	cfg, err := config.Load[config.Base]()
	if err != nil {
		return err
	}

	// --- Authn: dev HMAC verifier. Swap for authn.NewJWTVerifier against
	// your real IdP's JWKS endpoint in a non-example service. ---
	verifier := authn.NewHMACVerifier(devJWTSecret(), authn.ClaimsConfig{})

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

	// --- Storage: in-memory for this example; see store_pg.go for the
	// dbx/pgx-backed shape a real service would use instead. ---
	store := NewMemoryHeroStore()

	application, err := app.New(ctx, app.Options{
		Config:     cfg,
		Verifier:   verifier,
		Authorizer: authorizer,
		HealthChecks: []health.Check{
			{Name: "hero-store", Checker: func(context.Context) error { return nil }},
		},
	})
	if err != nil {
		return err
	}

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

	application.Mount("/v1/heroes", heroes)

	if cfg.Debug {
		dev := chi.NewRouter()
		dev.Post("/token", httpx.Handle(devTokenHandler(devJWTSecret(), cfg.Debug), cfg.Debug, httpx.WithStatus(http.StatusCreated)))
		application.MountPublic("/dev", dev)
		application.Logger.Warn("dev-token endpoint is mounted at POST /dev/token — debug mode only, never enable in production")
	}

	return application.ListenAndServe(ctx)
}
