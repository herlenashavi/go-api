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

	// Debug enables verbose logging, pretty (non-JSON) log output, and
	// exposes the OpenAPI/docs endpoints that are otherwise hidden.
	Debug bool `env:"DEBUG" envDefault:"false"`

	// HTTPHost/HTTPPort is the address the server binds to.
	HTTPHost string `env:"HTTP_HOST" envDefault:"0.0.0.0"`
	HTTPPort int    `env:"HTTP_PORT" envDefault:"8080"`

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

	// Authz: Cedar policy source.
	PolicyDir string `env:"POLICY_DIR" envDefault:"./policies"`
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
