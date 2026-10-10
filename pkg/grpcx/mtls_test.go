package grpcx_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/grpcx"
)

func parseCertPEM(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// TestMTLS_ValidCertEstablishesServiceIdentity proves the whole happy
// path: a client presenting a cert signed by the trusted CA connects
// successfully, and the server's peerIdentityInterceptor places the
// correct authn.ServiceIdentity (name from the cert, roles from
// TLSConfig.ServiceRoles) in context for the handler to see.
func TestMTLS_ValidCertEstablishesServiceIdentity(t *testing.T) {
	ca := newTestCA(t)
	serverCertPEM, serverKeyPEM := ca.issue(t, "villains")
	clientCertPEM, clientKeyPEM := ca.issue(t, "heroes")

	serverCertFile, serverKeyFile, caFile := writeCertFiles(t, serverCertPEM, serverKeyPEM, ca.certPEM)
	serverTLS, err := grpcx.LoadMTLS(grpcx.TLSConfig{
		CertFile:          serverCertFile,
		KeyFile:           serverKeyFile,
		CAFile:            caFile,
		RequireClientCert: true,
		ServiceRoles:      map[string][]string{"heroes": {"caller"}},
	})
	if err != nil {
		t.Fatalf("LoadMTLS (server): %v", err)
	}

	clientCertFile, clientKeyFile, _ := writeCertFiles(t, clientCertPEM, clientKeyPEM, ca.certPEM)
	clientTLS, err := grpcx.LoadMTLS(grpcx.TLSConfig{CertFile: clientCertFile, KeyFile: clientKeyFile, CAFile: caFile})
	if err != nil {
		t.Fatalf("LoadMTLS (client): %v", err)
	}

	var captured *authn.ServiceIdentity
	capture := grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		captured, _ = authn.ServiceIdentityFromContext(ctx)
		return handler(ctx, req)
	})

	server := grpcx.NewServer(grpcx.Dependencies{MTLS: serverTLS}, capture)
	grpcx.RegisterHealth(server, nil, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{MTLS: clientTLS})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v, want SERVING", resp.Status)
	}

	if captured == nil {
		t.Fatal("expected a ServiceIdentity in context, got none")
	}
	if captured.Name != "heroes" {
		t.Fatalf("Name = %q, want %q", captured.Name, "heroes")
	}
	if !captured.HasRole("caller") {
		t.Fatalf("expected role %q, got roles %v", "caller", captured.Roles)
	}
	if captured.Cert == nil || captured.Cert.Subject.CommonName != "heroes" {
		t.Fatalf("expected Cert to be the client's own leaf cert, got %+v", captured.Cert)
	}
}

// TestMTLS_RequireClientCert_RejectsPlaintextClient confirms a server
// requiring a client certificate refuses a caller that doesn't present
// one at all (here: a plain insecure-transport Dial against a
// TLS-terminated server, which fails the handshake outright).
func TestMTLS_RequireClientCert_RejectsPlaintextClient(t *testing.T) {
	ca := newTestCA(t)
	serverCertPEM, serverKeyPEM := ca.issue(t, "villains")
	serverCertFile, serverKeyFile, caFile := writeCertFiles(t, serverCertPEM, serverKeyPEM, ca.certPEM)

	serverTLS, err := grpcx.LoadMTLS(grpcx.TLSConfig{
		CertFile: serverCertFile, KeyFile: serverKeyFile, CAFile: caFile, RequireClientCert: true,
	})
	if err != nil {
		t.Fatalf("LoadMTLS: %v", err)
	}

	server := grpcx.NewServer(grpcx.Dependencies{MTLS: serverTLS})
	grpcx.RegisterHealth(server, nil, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	// No MTLS configured client-side -> insecure transport against a
	// TLS-only server.
	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("expected an error connecting with no TLS to an mTLS-required server")
	}
}

// TestMTLS_UntrustedClientCertRejected confirms a client certificate
// signed by a CA the server doesn't trust is refused — the actual point
// of mTLS, as opposed to merely "any cert accepted."
func TestMTLS_UntrustedClientCertRejected(t *testing.T) {
	serverCA := newTestCA(t)
	otherCA := newTestCA(t) // signs the client's cert; the server doesn't trust this CA

	serverCertPEM, serverKeyPEM := serverCA.issue(t, "villains")
	serverCertFile, serverKeyFile, serverCAFile := writeCertFiles(t, serverCertPEM, serverKeyPEM, serverCA.certPEM)
	serverTLS, err := grpcx.LoadMTLS(grpcx.TLSConfig{
		CertFile: serverCertFile, KeyFile: serverKeyFile, CAFile: serverCAFile, RequireClientCert: true,
	})
	if err != nil {
		t.Fatalf("LoadMTLS (server): %v", err)
	}

	// Client's own cert is signed by otherCA (untrusted by the server),
	// but the client trusts serverCA so *it* can verify the server fine
	// — isolating the failure to exactly "server rejects this client cert."
	clientCertPEM, clientKeyPEM := otherCA.issue(t, "imposter")
	clientCertFile, clientKeyFile, _ := writeCertFiles(t, clientCertPEM, clientKeyPEM, otherCA.certPEM)
	clientCAFile := filepath.Join(filepath.Dir(clientCertFile), "ca-trust.pem")
	if err := os.WriteFile(clientCAFile, serverCA.certPEM, 0o600); err != nil {
		t.Fatalf("write client CA trust file: %v", err)
	}
	clientTLS, err := grpcx.LoadMTLS(grpcx.TLSConfig{CertFile: clientCertFile, KeyFile: clientKeyFile, CAFile: clientCAFile})
	if err != nil {
		t.Fatalf("LoadMTLS (client): %v", err)
	}

	server := grpcx.NewServer(grpcx.Dependencies{MTLS: serverTLS})
	grpcx.RegisterHealth(server, nil, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{MTLS: clientTLS})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("expected an error connecting with a client cert signed by an untrusted CA")
	}
}

// TestMTLS_Reload proves LoadMTLS's result actually hot-reloads: a new
// TLS handshake after calling Reload presents the new leaf certificate,
// not the one loaded at startup — exercising serverTLSConfig's
// GetConfigForClient hook for real, over a real socket, rather than
// asserting anything about its unexported internals directly.
func TestMTLS_Reload(t *testing.T) {
	ca := newTestCA(t)
	cert1PEM, key1PEM := ca.issue(t, "villains-v1")
	certFile, keyFile, caFile := writeCertFiles(t, cert1PEM, key1PEM, ca.certPEM)

	store, err := grpcx.LoadMTLS(grpcx.TLSConfig{CertFile: certFile, KeyFile: keyFile, CAFile: caFile})
	if err != nil {
		t.Fatalf("LoadMTLS: %v", err)
	}

	server := grpcx.NewServer(grpcx.Dependencies{MTLS: store})
	grpcx.RegisterHealth(server, nil, time.Second)
	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(ca.certPEM)
	probe := func() *x509.Certificate {
		conn, err := tls.Dial("tcp", lis.Addr().String(), &tls.Config{RootCAs: caPool, ServerName: "127.0.0.1"})
		if err != nil {
			t.Fatalf("tls.Dial: %v", err)
		}
		defer conn.Close()
		state := conn.ConnectionState()
		if len(state.PeerCertificates) == 0 {
			t.Fatal("no peer certificates presented")
		}
		return state.PeerCertificates[0]
	}

	cert1 := parseCertPEM(t, cert1PEM)
	if got := probe(); got.SerialNumber.Cmp(cert1.SerialNumber) != 0 {
		t.Fatalf("before reload: serial = %v, want %v", got.SerialNumber, cert1.SerialNumber)
	}

	cert2PEM, key2PEM := ca.issue(t, "villains-v2")
	if err := os.WriteFile(certFile, cert2PEM, 0o600); err != nil {
		t.Fatalf("overwrite cert file: %v", err)
	}
	if err := os.WriteFile(keyFile, key2PEM, 0o600); err != nil {
		t.Fatalf("overwrite key file: %v", err)
	}
	if err := store.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	cert2 := parseCertPEM(t, cert2PEM)
	if got := probe(); got.SerialNumber.Cmp(cert2.SerialNumber) != 0 {
		t.Fatalf("after reload: serial = %v, want %v (still serving the old cert)", got.SerialNumber, cert2.SerialNumber)
	}
}

// TestMTLS_Watch confirms the polling path (Watch), not just a manually
// called Reload, picks up a changed cert on its own.
func TestMTLS_Watch(t *testing.T) {
	ca := newTestCA(t)
	cert1PEM, key1PEM := ca.issue(t, "villains-v1")
	certFile, keyFile, caFile := writeCertFiles(t, cert1PEM, key1PEM, ca.certPEM)

	store, err := grpcx.LoadMTLS(grpcx.TLSConfig{CertFile: certFile, KeyFile: keyFile, CAFile: caFile})
	if err != nil {
		t.Fatalf("LoadMTLS: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go store.Watch(ctx, 20*time.Millisecond, nil)

	server := grpcx.NewServer(grpcx.Dependencies{MTLS: store})
	grpcx.RegisterHealth(server, nil, time.Second)
	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(ca.certPEM)
	probe := func() *x509.Certificate {
		conn, err := tls.Dial("tcp", lis.Addr().String(), &tls.Config{RootCAs: caPool, ServerName: "127.0.0.1"})
		if err != nil {
			t.Fatalf("tls.Dial: %v", err)
		}
		defer conn.Close()
		return conn.ConnectionState().PeerCertificates[0]
	}

	// touch the files slightly in the future so the modtime poll
	// reliably observes a change even on coarse filesystem clocks.
	cert2PEM, key2PEM := ca.issue(t, "villains-v2")
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(certFile, cert2PEM, 0o600); err != nil {
		t.Fatalf("overwrite cert file: %v", err)
	}
	if err := os.WriteFile(keyFile, key2PEM, 0o600); err != nil {
		t.Fatalf("overwrite key file: %v", err)
	}

	cert2 := parseCertPEM(t, cert2PEM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if probe().SerialNumber.Cmp(cert2.SerialNumber) == 0 {
			return // picked up the change on its own
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Watch never picked up the rotated certificate")
}
