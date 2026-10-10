package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	villainsv1 "github.com/herlenashavi/go-api/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/health"
)

// devJWTSecret mirrors examples/heroes's function of the same name
// exactly, deliberately: same env var, same default, so a token minted by
// either service's HMAC verifier is accepted by both. See that function's
// doc comment in examples/heroes/main.go.
func devJWTSecret() []byte {
	if s := os.Getenv("DEV_JWT_SECRET"); s != "" {
		return []byte(s)
	}
	return []byte("dev-secret-change-me")
}

func grpcPort() int {
	if s := os.Getenv("GRPC_PORT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 9091
}

// loadMTLS returns nil, nil when mTLS isn't configured — mutual TLS is
// entirely opt-in for this example, so `go run .` keeps working with
// zero setup. Generate dev certs with:
//
//	go run ./deploy/mtls/gencerts
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
func loadMTLS() (*grpcx.MTLSStore, error) {
	certFile, keyFile, caFile := os.Getenv("MTLS_CERT_FILE"), os.Getenv("MTLS_KEY_FILE"), os.Getenv("MTLS_CA_FILE")
	if certFile == "" && keyFile == "" && caFile == "" {
		return nil, nil
	}
	return grpcx.LoadMTLS(grpcx.TLSConfig{
		CertFile:          certFile,
		KeyFile:           keyFile,
		CAFile:            caFile,
		RequireClientCert: os.Getenv("MTLS_REQUIRE_CLIENT_CERT") == "true",
		ServiceRoles:      map[string][]string{"heroes": {"trusted-caller"}},
	})
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

	verifier := authn.NewHMACVerifier(devJWTSecret(), authn.ClaimsConfig{})

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

	mtls, err := loadMTLS()
	if err != nil {
		return err
	}

	application, err := app.New(ctx, app.Options{
		Config:      cfg,
		Verifier:    verifier,
		Authorizer:  authorizer,
		GRPCEnabled: true,
		GRPCPort:    grpcPort(),
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
