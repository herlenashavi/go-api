// Command gencerts generates a throwaway dev CA and one leaf certificate
// per named service, for trying mTLS between examples/heroes and
// examples/villains locally. It is not a certificate management tool —
// no rotation, no revocation, 24-hour validity — just enough to flip
// MTLS_CERT_FILE/MTLS_KEY_FILE/MTLS_CA_FILE on and see the real thing
// work, the same spirit as authn.NewHMACVerifier's dev-only token
// signing. See docs/grpc.md's mTLS section for what a real deployment
// needs instead (cert-manager, SPIRE, or similar).
//
// Usage:
//
//	go run ./deploy/mtls/gencerts -out deploy/mtls/certs -service heroes -service villains
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

type serviceList []string

func (s *serviceList) String() string { return fmt.Sprint(*s) }
func (s *serviceList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func run() error {
	var services serviceList
	outDir := flag.String("out", "deploy/mtls/certs", "output directory for ca.pem and <service>-{cert,key}.pem files")
	flag.Var(&services, "service", "service name to issue a cert for (repeatable)")
	flag.Parse()

	if len(services) == 0 {
		services = serviceList{"heroes", "villains"}
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	caKey, caCert, caPEM, err := generateCA()
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "ca.pem"), caPEM, 0o644); err != nil {
		return fmt.Errorf("write ca.pem: %w", err)
	}
	fmt.Printf("wrote %s\n", filepath.Join(*outDir, "ca.pem"))

	for _, name := range services {
		certPEM, keyPEM, err := issueLeaf(caKey, caCert, name)
		if err != nil {
			return fmt.Errorf("issue cert for %q: %w", name, err)
		}
		certPath := filepath.Join(*outDir, name+"-cert.pem")
		keyPath := filepath.Join(*outDir, name+"-key.pem")
		if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", certPath, err)
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", keyPath, err)
		}
		fmt.Printf("wrote %s, %s\n", certPath, keyPath)
	}

	fmt.Println("\nTo run heroes and villains with mTLS between them:")
	for _, name := range services {
		fmt.Printf("  MTLS_CERT_FILE=%s/%s-cert.pem MTLS_KEY_FILE=%s/%s-key.pem MTLS_CA_FILE=%s/ca.pem \\\n",
			*outDir, name, *outDir, name, *outDir)
	}
	return nil
}

func generateCA() (*ecdsa.PrivateKey, *x509.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "go-api dev CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return key, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func issueLeaf(caKey *ecdsa.PrivateKey, caCert *x509.Certificate, commonName string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost", commonName},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
