package config_test

import (
	"os"
	"testing"

	"github.com/herlenashavi/go-api/pkg/config"
)

// unsetEnv removes key for the duration of the test and restores
// whatever it was (set or unset) afterward — t.Setenv can only set a
// value, not remove the var entirely, and the `required` tag's check is
// presence-based (os.LookupEnv), so a present-but-empty value doesn't
// trigger it the way an actually-unset one does.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%q): %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		}
	})
}

func TestLoad_RequiredFieldMissing(t *testing.T) {
	unsetEnv(t, "SERVICE_NAME")
	if _, err := config.Load[config.Base](); err == nil {
		t.Fatal("expected an error when required SERVICE_NAME is unset")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "svc")

	cfg, err := config.Load[config.Base]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := map[string]struct {
		got  any
		want any
	}{
		"Env":          {cfg.Env, "local"},
		"Debug":        {cfg.Debug, false},
		"HTTPHost":     {cfg.HTTPHost, "0.0.0.0"},
		"HTTPPort":     {cfg.HTTPPort, 8080},
		"GRPCHost":     {cfg.GRPCHost, ""},
		"GRPCPort":     {cfg.GRPCPort, 9091},
		"LogLevel":     {cfg.LogLevel, "info"},
		"PolicyDir":    {cfg.PolicyDir, "./policies"},
		"DevJWTSecret": {cfg.DevJWTSecret, "dev-secret-change-me"},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
	}

	if cfg.MTLS.Enabled() {
		t.Error("MTLS.Enabled() = true with no MTLS_* env vars set, want false")
	}
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	t.Setenv("SERVICE_NAME", "svc")
	t.Setenv("HTTP_PORT", "9000")
	t.Setenv("GRPC_PORT", "7000")
	t.Setenv("DEV_JWT_SECRET", "a-real-secret")
	t.Setenv("MTLS_CERT_FILE", "/tmp/cert.pem")
	t.Setenv("MTLS_KEY_FILE", "/tmp/key.pem")
	t.Setenv("MTLS_CA_FILE", "/tmp/ca.pem")
	t.Setenv("MTLS_REQUIRE_CLIENT_CERT", "true")

	cfg, err := config.Load[config.Base]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTPPort != 9000 {
		t.Errorf("HTTPPort = %d, want 9000", cfg.HTTPPort)
	}
	if cfg.GRPCPort != 7000 {
		t.Errorf("GRPCPort = %d, want 7000", cfg.GRPCPort)
	}
	if cfg.DevJWTSecret != "a-real-secret" {
		t.Errorf("DevJWTSecret = %q, want %q", cfg.DevJWTSecret, "a-real-secret")
	}
	if !cfg.MTLS.Enabled() {
		t.Error("MTLS.Enabled() = false with all three MTLS_* files set, want true")
	}
	if cfg.MTLS.CertFile != "/tmp/cert.pem" || cfg.MTLS.KeyFile != "/tmp/key.pem" || cfg.MTLS.CAFile != "/tmp/ca.pem" {
		t.Errorf("MTLS = %+v, want cert/key/ca files from env", cfg.MTLS)
	}
	if !cfg.MTLS.RequireClientCert {
		t.Error("MTLS.RequireClientCert = false, want true")
	}
}

func TestMTLSConfig_Enabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.MTLSConfig
		want bool
	}{
		{"all empty", config.MTLSConfig{}, false},
		{"cert only", config.MTLSConfig{CertFile: "c.pem"}, true},
		{"key only", config.MTLSConfig{KeyFile: "k.pem"}, true},
		{"ca only", config.MTLSConfig{CAFile: "ca.pem"}, true},
		{"all set", config.MTLSConfig{CertFile: "c.pem", KeyFile: "k.pem", CAFile: "ca.pem"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.Enabled(); got != c.want {
				t.Errorf("Enabled() = %v, want %v", got, c.want)
			}
		})
	}
}

// Settings mirrors the extension pattern documented on config.Base: a
// service embeds Base and adds its own typed, env-tagged fields rather
// than reading extra env vars by hand (see examples/heroes/main.go's
// Settings for the real-world version of this).
type settings struct {
	config.Base
	KafkaBrokers []string `env:"KAFKA_BROKERS" envSeparator:","`
}

func TestLoad_EmbeddingExtendsBase(t *testing.T) {
	t.Setenv("SERVICE_NAME", "svc")
	t.Setenv("KAFKA_BROKERS", "broker-a:9092,broker-b:9092")

	cfg, err := config.Load[settings]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ServiceName != "svc" {
		t.Errorf("ServiceName = %q, want %q (inherited from embedded Base)", cfg.ServiceName, "svc")
	}
	want := []string{"broker-a:9092", "broker-b:9092"}
	if len(cfg.KafkaBrokers) != len(want) || cfg.KafkaBrokers[0] != want[0] || cfg.KafkaBrokers[1] != want[1] {
		t.Errorf("KafkaBrokers = %v, want %v", cfg.KafkaBrokers, want)
	}
}

func TestMustLoad_PanicsOnError(t *testing.T) {
	unsetEnv(t, "SERVICE_NAME")

	defer func() {
		if recover() == nil {
			t.Fatal("expected MustLoad to panic when a required field is missing")
		}
	}()
	config.MustLoad[config.Base]()
}
