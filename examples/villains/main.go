package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/health"
)

// loadMTLS wraps grpcx.LoadMTLSFromConfig with this service's
// ServiceRoles: which roles a calling service's identity gets is an
// authorization decision, the same category as the RoleRegistry below,
// so it's code here rather than an env var. Returns nil, nil when mTLS
// isn't configured (cfg.MTLS all empty) — entirely opt-in, so `go run .`
// keeps working with zero setup. Generate dev certs with:
//
//	go run ./deploy/mtls/gencerts -service heroes -service villains
//
// and set the three env vars it prints to turn this on.
//
// This example's RPC handlers (server.go) authorize purely against the
// forwarded user token (authn.Principal), same as without mTLS — turning
// this on adds transport-level mutual authentication and encryption
// without changing any authorization decision. ServiceRoles is wired up
// here (so a real authn.ServiceIdentity with this role does land in
// context) but nothing in server.go reads it; see
// pkg/authz/authorizer_test.go's CheckService tests and docs/grpc.md's
// mTLS section for a worked example of authorizing by calling-service
// identity instead of (or alongside) a forwarded user token.
func loadMTLS(cfg config.MTLSConfig) (*grpcx.MTLSStore, error) {
	return grpcx.LoadMTLSFromConfig(cfg, map[string][]string{"heroes": {"trusted-caller"}})
}

func main() {
	setDefaultEnv("SERVICE_NAME", "villains-example")
	setDefaultEnv("DEBUG", "true")
	setDefaultEnv("HTTP_PORT", "8081") // distinct from heroes's 8080 so both run on one machine

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("villains-example exited with error", "error", err)
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

	verifier := authn.NewHMACVerifier([]byte(cfg.DevJWTSecret), authn.ClaimsConfig{})

	// Same role names as examples/heroes, but this RoleRegistry and
	// PolicyStore are entirely this service's own — see
	// policies/villains.cedar's package doc.
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

	store := NewVillainStore()

	mtls, err := loadMTLS(cfg.MTLS)
	if err != nil {
		return err
	}

	application, err := app.New(ctx, app.Options{
		Config:      cfg,
		Verifier:    verifier,
		Authorizer:  authorizer,
		GRPCEnabled: true,
		GRPCMTLS:    mtls,
		HealthChecks: []health.Check{
			{Name: "villain-store", Checker: func(context.Context) error { return nil }},
		},
	})
	if err != nil {
		return err
	}
	if mtls != nil {
		go mtls.Watch(ctx, 30*time.Second, application.Logger)
		application.Logger.Info("mTLS enabled for the gRPC server")
	}

	villainsv1.RegisterVillainServiceServer(application.GRPCServer, NewVillainServer(store, authorizer))

	// See examples/heroes/main.go's identical call for why this exists
	// and what drives the latency when policies/villains.cedar changes
	// under deploy/tilt.
	go policyStore.WatchAndReload(ctx, 5*time.Second, application.Logger)

	return application.ListenAndServe(ctx)
}
