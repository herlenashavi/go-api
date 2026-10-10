package grpcx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// TLSConfig configures mutual TLS for service-to-service calls: a
// server built with it requires (or accepts) a client certificate and
// verifies it against CAFile; a client built with it presents
// CertFile/KeyFile and verifies the server against CAFile. Both sides
// use the same three files by design — in the common case, every
// service in a trust domain is issued a cert by the same internal CA.
//
// This is deliberately separate from bearer-token auth (pkg/authn,
// authnInterceptor): mTLS authenticates *which service* is calling,
// verified by the TLS handshake itself; a bearer token authenticates
// *which user* a call is being made on behalf of. A call can carry
// either, both, or neither — see authn.ServiceIdentity's doc comment for
// the shapes that make sense in practice.
type TLSConfig struct {
	// CertFile/KeyFile are this service's own certificate and private
	// key (PEM), presented to peers.
	CertFile string
	KeyFile  string
	// CAFile (PEM, may contain multiple certs) is the trust root used to
	// verify the *peer's* certificate — the CA that signs every
	// service's CertFile in this trust domain.
	CAFile string

	// RequireClientCert, server-side only, rejects any connection that
	// doesn't present a client certificate verifiable against CAFile.
	// Default false: a server can accept mTLS from callers that have it
	// while still serving callers that don't (useful while rolling mTLS
	// out incrementally across a fleet) — set true once every caller is
	// expected to present one.
	RequireClientCert bool

	// ServiceRoles maps a verified peer identity's Name (see
	// authn.ServiceIdentity) to the roles it's granted. Roles aren't read
	// from the certificate itself — a cert proves identity, not
	// authorization — this is the same "roles come from your own config,
	// not the credential" shape as authn.Principal.Roles. A name with no
	// entry gets no roles (it can still be matched by identity in a
	// policy, e.g. `principal == Service::"heroes"`, just not by role).
	ServiceRoles map[string][]string
}

func (c TLSConfig) validate() error {
	if c.CertFile == "" || c.KeyFile == "" || c.CAFile == "" {
		return fmt.Errorf("grpcx: TLSConfig requires CertFile, KeyFile, and CAFile")
	}
	return nil
}

// certMaterial is the atomically-swapped unit: a leaf cert plus the CA
// pool used to verify peers, always replaced together so a handshake
// never sees one updated without the other.
type certMaterial struct {
	cert   tls.Certificate
	caPool *x509.CertPool
}

// MTLSStore holds the currently-loaded certificate/CA material for one
// TLSConfig and knows how to reload it from disk — the mTLS counterpart
// to authz.PolicyStore: LoadMTLS loads once at startup (failing fast on
// a bad config, same reasoning as authz.LoadPolicyDir), and the caller
// decides whether to additionally spawn Watch for hot reload, the exact
// same shape as calling policyStore.WatchAndReload.
type MTLSStore struct {
	cfg     TLSConfig
	current atomic.Pointer[certMaterial]
}

// LoadMTLS reads cfg's cert/key/CA files once. Pass the result as
// Dependencies.MTLS to NewServer and/or Dial.
func LoadMTLS(cfg TLSConfig) (*MTLSStore, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &MTLSStore{cfg: cfg}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads the cert/key/CA files and atomically swaps in the new
// material. Called by Watch on a timer; exported so a caller that'd
// rather reload on an explicit signal (SIGHUP, a file-watcher event) than
// a poll loop can do that instead.
func (s *MTLSStore) Reload() error {
	cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("grpcx: load cert/key pair: %w", err)
	}

	caPEM, err := os.ReadFile(s.cfg.CAFile)
	if err != nil {
		return fmt.Errorf("grpcx: read CA file %q: %w", s.cfg.CAFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("grpcx: no valid certificates found in CA file %q", s.cfg.CAFile)
	}

	s.current.Store(&certMaterial{cert: cert, caPool: pool})
	return nil
}

func (s *MTLSStore) get() *certMaterial {
	return s.current.Load()
}

// Watch polls the cert/key/CA files every interval and reloads on
// change, until ctx is canceled — mirrors
// authz.PolicyStore.WatchAndReload exactly, including why: certificates
// get rotated out from under a long-lived process (cert-manager, SPIRE,
// a renewal cron job), and it shouldn't need a restart to pick up a
// renewed one. A failed reload is logged and the previous, known-good
// material keeps serving — a transient read error (e.g. a rotation tool
// caught mid-write) never takes down live connections.
//
//	store, err := grpcx.LoadMTLS(cfg)
//	// ...
//	go store.Watch(ctx, 30*time.Second, application.Logger)
func (s *MTLSStore) Watch(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	lastModTime := s.modTime()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			modTime := s.modTime()
			if modTime.Equal(lastModTime) {
				continue
			}
			lastModTime = modTime
			if err := s.Reload(); err != nil {
				logger.Error("grpcx: mTLS cert reload failed, keeping previous cert material", "error", err)
				continue
			}
			logger.Info("grpcx: mTLS cert material reloaded")
		}
	}
}

func (s *MTLSStore) modTime() time.Time {
	latest := time.Time{}
	for _, path := range []string{s.cfg.CertFile, s.cfg.KeyFile, s.cfg.CAFile} {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// serverTLSConfig returns a *tls.Config whose GetConfigForClient hook
// reads s's current material on every handshake — the standard Go
// pattern for hot-reloadable server TLS material, since tls.Config's
// plain Certificates/ClientCAs fields are fixed at construction time.
func (s *MTLSStore) serverTLSConfig() *tls.Config {
	clientAuth := tls.VerifyClientCertIfGiven
	if s.cfg.RequireClientCert {
		clientAuth = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			m := s.get()
			return &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{m.cert},
				ClientCAs:    m.caPool,
				ClientAuth:   clientAuth,
			}, nil
		},
	}
}

// clientTLSConfig returns a *tls.Config for dialing serverName. The
// client's own certificate reloads on every handshake
// (GetClientCertificate) the same way the server's does; the CA pool
// used to verify the *server* is fixed at construction time instead —
// root-of-trust material changes far less often than individual service
// leaf certs do, and grpc's ClientConn doesn't offer a clean way to swap
// transport credentials after Dial without forcing a reconnect, so
// hot-reloading it wouldn't actually take effect on a long-lived
// connection anyway. If your deployment rotates the CA itself (not just
// leaf certs), redial rather than expecting this to pick it up live.
func (s *MTLSStore) clientTLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		RootCAs:    s.get().caPool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &s.get().cert, nil
		},
	}
}

// peerIdentityName derives a authn.ServiceIdentity.Name from a verified
// peer certificate: the first URI SAN if it looks like a SPIFFE ID
// (spiffe://...), falling back to the Subject Common Name. Roles are
// filled in by the caller from TLSConfig.ServiceRoles, since a
// certificate proves identity, never authorization.
func peerIdentityName(cert *x509.Certificate) string {
	for _, uri := range cert.URIs {
		if strings.HasPrefix(uri.Scheme, "spiffe") {
			return uri.String()
		}
	}
	return cert.Subject.CommonName
}
