// Package config provides environment-driven configuration loading for
// services built on go-api. It intentionally does no magic: every field is a
// plain struct field with an `env` tag, loaded once at startup.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Base holds the settings every go-api service needs. Embed it in your own
// settings struct to extend it:
//
//	type Settings struct {
//		config.Base
//		KafkaBrokers []string `env:"KAFKA_BROKERS" envSeparator:","`
//	}
type Base struct {
	// ServiceName identifies the service in logs, traces, and metrics.
	ServiceName string `env:"SERVICE_NAME,required"`

	// Env is the deployment environment (e.g. "local", "staging", "prod").
	// It is attached as a resource attribute on every trace and log line.
	Env string `env:"ENV" envDefault:"local"`

	// Debug enables debug-level logging and request-body logging, and
	// exposes the OpenAPI/docs endpoints that are otherwise hidden. It does
	// NOT change the log format — that's always JSON, see pkg/logging.
	Debug bool `env:"DEBUG" envDefault:"false"`

	// HTTPHost/HTTPPort is the address the HTTP server binds to.
	HTTPHost string `env:"HTTP_HOST" envDefault:"0.0.0.0"`
	HTTPPort int    `env:"HTTP_PORT" envDefault:"8080"`

	// GRPCHost/GRPCPort is the address the gRPC server binds to, used only
	// by a service that opts into app.Options.GRPCEnabled — whether to run
	// a gRPC server at all is a per-service code decision, not an env one,
	// but once a service has one, its listen address is exactly as
	// deployment-tunable as HTTPHost/HTTPPort above. GRPCHost empty
	// defaults to HTTPHost (see app.New).
	GRPCHost string `env:"GRPC_HOST" envDefault:""`
	GRPCPort int    `env:"GRPC_PORT" envDefault:"9091"`

	// APIPrefix is prepended to every mounted route (e.g. "/v1").
	APIPrefix string `env:"API_PREFIX" envDefault:""`

	// LogLevel is one of debug|info|warn|error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	// ShutdownTimeout bounds how long graceful shutdown waits for in-flight
	// requests to drain before the process exits.
	ShutdownTimeoutSeconds int `env:"SHUTDOWN_TIMEOUT_SECONDS" envDefault:"15"`

	// RequestTimeoutSeconds is the default per-request deadline applied by
	// the timeout middleware. Individual routes may override it.
	RequestTimeoutSeconds int `env:"REQUEST_TIMEOUT_SECONDS" envDefault:"30"`

	// HTTPReadTimeoutSeconds bounds how long the server spends reading an
	// entire request, headers and body — the guard against a client that
	// trickles a body in slowly enough to pin a connection and its
	// goroutine (RequestTimeoutSeconds can't, since a handler's blocked
	// Body.Read doesn't observe the request context). Zero disables it.
	HTTPReadTimeoutSeconds int `env:"HTTP_READ_TIMEOUT_SECONDS" envDefault:"30"`

	// HTTPIdleTimeoutSeconds bounds how long an idle keep-alive
	// connection is held open between requests. Zero falls back to
	// HTTPReadTimeoutSeconds (net/http's own rule).
	HTTPIdleTimeoutSeconds int `env:"HTTP_IDLE_TIMEOUT_SECONDS" envDefault:"120"`

	// MaxBodyBytes caps request body size. Zero disables the limit (not
	// recommended).
	MaxBodyBytes int64 `env:"MAX_BODY_BYTES" envDefault:"1048576"`

	// CORSAllowedOrigins is a comma-separated allowlist. Empty means no
	// cross-origin requests are allowed (safer default than "*").
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`

	// DatabaseURL is a postgres:// connection string. Empty if the service
	// does not use dbx.
	DatabaseURL string `env:"DATABASE_URL"`

	// OTel / tracing.
	OTLPEndpoint    string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:""`
	OTLPInsecure    bool    `env:"OTEL_EXPORTER_OTLP_INSECURE" envDefault:"true"`
	TracingEnabled  bool    `env:"TRACING_ENABLED" envDefault:"false"`
	MetricsEnabled  bool    `env:"METRICS_ENABLED" envDefault:"true"`
	TraceSampleRate float64 `env:"TRACE_SAMPLE_RATIO" envDefault:"1.0"`

	// Auth: JWT/OIDC verification.
	JWTIssuer   string `env:"JWT_ISSUER"`
	JWTAudience string `env:"JWT_AUDIENCE"`
	JWKSURL     string `env:"JWKS_URL"`

	// DevJWTSecret is the shared secret for authn.NewHMACVerifier, the
	// dev-only JWT verifier used in place of a real IdP (see that
	// function's doc comment for why it's never the production path).
	// The default is intentionally well-known — this field only matters
	// for local dev and example services, where "works out of the box"
	// beats a random value nobody can reproduce. Set DEV_JWT_SECRET
	// yourself to any value and every service that reads it will mint and
	// verify tokens consistently. Because the default is public, app.New
	// refuses an HMAC verifier using it (or an empty secret) unless Debug
	// is set — see authn.UsesPublicDevSecret. Keep this default in sync
	// with authn.DefaultDevSecret.
	DevJWTSecret string `env:"DEV_JWT_SECRET" envDefault:"dev-secret-change-me"`

	// Authz: Cedar policy source.
	PolicyDir string `env:"POLICY_DIR" envDefault:"./policies"`

	// MTLS configures mutual TLS for this service's gRPC server and/or
	// its outbound gRPC clients — see grpcx.LoadMTLSFromConfig, which
	// turns this into a *grpcx.MTLSStore, and grpcx.TLSConfig, whose
	// CertFile/KeyFile/CAFile/RequireClientCert fields these mirror
	// exactly. Left unset (all three file fields empty), mTLS is off and
	// every service keeps working with zero setup.
	MTLS MTLSConfig
}

// MTLSConfig is config.Base's mTLS settings. See Base.MTLS's doc comment.
type MTLSConfig struct {
	// CertFile/KeyFile are this service's own certificate and private key
	// (PEM), presented to peers.
	CertFile string `env:"MTLS_CERT_FILE"`
	KeyFile  string `env:"MTLS_KEY_FILE"`
	// CAFile (PEM) is the trust root used to verify a peer's certificate.
	CAFile string `env:"MTLS_CA_FILE"`
	// RequireClientCert, server-side only, rejects any connection that
	// doesn't present a client certificate verifiable against CAFile. See
	// grpcx.TLSConfig.RequireClientCert's doc comment for the default-off
	// rationale (incremental rollout across a fleet).
	RequireClientCert bool `env:"MTLS_REQUIRE_CLIENT_CERT" envDefault:"false"`
}

// Enabled reports whether any mTLS file is configured. All three empty is
// the common "mTLS off" case every service's loadMTLS-style helper used to
// check by hand; grpcx.LoadMTLSFromConfig does that check with this method
// instead.
func (c MTLSConfig) Enabled() bool {
	return c.CertFile != "" || c.KeyFile != "" || c.CAFile != ""
}

// Load parses environment variables into T, which must embed Base (directly
// or transitively). It fails fast with a descriptive error rather than
// starting the service with a half-populated config.
//
//	type Settings struct{ config.Base }
//	cfg, err := config.Load[Settings]()
func Load[T any]() (T, error) {
	var cfg T
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// MustLoad is Load but panics on error. Intended for use in main() only,
// where there is no reasonable recovery path from a missing env var.
func MustLoad[T any]() T {
	cfg, err := Load[T]()
	if err != nil {
		panic(err)
	}
	return cfg
}
