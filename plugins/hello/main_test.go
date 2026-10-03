// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/rpc"
	"os"
	"strings"
	"testing"
	"time"
)

// testca kapselt eine im Test erzeugte PKI (CA + Leaf + Client), damit die
// Tests den echten mTLS-Pfad durchlaufen statt Attrappen zu verwenden.
type testCA struct {
	caPEM     []byte
	leafCert  []byte // PEM
	leafKey   []byte // PEM
	leafTLSCr tls.Certificate
	clientCr  tls.Certificate
	pool      *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("CA-Key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "hello-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CA-Zertifikat: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("CA-Parse: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	makeLeaf := func(cn string, usages []x509.ExtKeyUsage) (certPEM, keyPEM []byte, crt tls.Certificate) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("Leaf-Key: %v", err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  usages,
			DNSNames:     []string{"hello-plugin", "localhost"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatalf("Leaf-Zertifikat: %v", err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("Leaf-Key-Marshal: %v", err)
		}
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatalf("X509KeyPair: %v", err)
		}
		return certPEM, keyPEM, pair
	}

	leafCert, leafKey, leafPair := makeLeaf("hello-plugin", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	_, _, clientPair := makeLeaf("host-client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	return &testCA{
		caPEM:     caPEM,
		leafCert:  leafCert,
		leafKey:   leafKey,
		leafTLSCr: leafPair,
		clientCr:  clientPair,
		pool:      pool,
	}
}

// setEnv setzt die vom Plugin erwarteten Variablen im Windows-Transport
// (base64-PEM) und stellt alle vier nach dem Test wieder her.
func (c *testCA) setEnv(t *testing.T, withCert, withKey, withCA bool) {
	t.Helper()
	keys := []string{envCertPEM, envKeyPEM, envCAPEM, envTLSMinVersion}
	for _, key := range keys {
		old, had := os.LookupEnv(key)
		key, old, had := key, old, had
		t.Cleanup(func() {
			if had {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	os.Unsetenv(envTLSMinVersion)
	if withCert {
		os.Setenv(envCertPEM, base64.StdEncoding.EncodeToString(c.leafCert))
	} else {
		os.Unsetenv(envCertPEM)
	}
	if withKey {
		os.Setenv(envKeyPEM, base64.StdEncoding.EncodeToString(c.leafKey))
	} else {
		os.Unsetenv(envKeyPEM)
	}
	if withCA {
		os.Setenv(envCAPEM, base64.StdEncoding.EncodeToString(c.caPEM))
	} else {
		os.Unsetenv(envCAPEM)
	}
}

// startPlugin startet run() in einer Goroutine, fängt die READY-Zeile aus der
// echten stdout-Pipe des Prozesses ab und liefert Adresse + Abbau-Funktion.
// os.Pipe wird genutzt, damit exakt die real gedruckte Zeile geprüft wird.
func startPlugin(t *testing.T) (string, func()) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan error, 1)
	go func() { done <- run() }()

	lineCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(r)
		if sc.Scan() {
			lineCh <- sc.Text()
			return
		}
		lineCh <- ""
	}()

	var line string
	select {
	case line = <-lineCh:
	case <-time.After(5 * time.Second):
		os.Stdout = old
		t.Fatal("Timeout: keine READY-Zeile innerhalb 5s")
	}
	// stdout wiederherstellen, damit spaetere Ausgaben (Shutdown-Log) sichtbar
	// sind und die Pipe nicht blockiert. Der Listener lebt bereits.
	os.Stdout = old
	_ = w.Close()

	if !strings.HasPrefix(line, readyPrefix+" ") {
		t.Fatalf("READY-Zeile falsches Format: %q", line)
	}
	addr := strings.TrimSpace(strings.TrimPrefix(line, readyPrefix+" "))
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatalf("READY-Adresse nicht host:port: %q", addr)
	}

	cleanup := func() {
		select {
		case <-done:
		default:
			// Kein Signal-Kanal im Test; Prozessende wird nicht erzwungen.
		}
		_ = r.Close()
	}
	return addr, cleanup
}

// TestHello_ReadyLineFormat prüft, dass die Ready-Zeile exakt dem
// Host-Vertrag entspricht: Präfix + genau eine loopback-Adresse mit einem
// Port im akzeptierten Bereich (1024-65535).
func TestHello_ReadyLineFormat(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	if got := strings.Count(addr, ":"); got != 1 {
		t.Fatalf("Adresse soll genau ein ':' enthalten, war %q", addr)
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	if host != "127.0.0.1" {
		t.Errorf("Host = %q, erwartet 127.0.0.1", host)
	}
	if !strings.HasPrefix(portStr, "") || portStr == "" {
		t.Fatalf("leerer Port in %q", addr)
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		t.Fatalf("ResolveTCPAddr: %v", err)
	}
	if tcpAddr.Port < 1024 || tcpAddr.Port > 65535 {
		t.Errorf("Port %d ausserhalb 1024-65535", tcpAddr.Port)
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Errorf("IP %v ist nicht loopback", tcpAddr.IP)
	}
}

// TestHello_HandshakePing baut eine echte gegenseitige mTLS-Verbindung zum
// laufenden Plugin auf und ruft HandshakeService.Ping wie der Host auf.
// Erwartet Basis-Version "1.0".
func TestHello_HandshakePing(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	conn, err := tls.Dial("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{ca.clientCr},
		RootCAs:      ca.pool,
		ServerName:   "hello-plugin",
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("mTLS-Dial: %v", err)
	}
	defer conn.Close()

	client := rpc.NewClient(conn)
	defer client.Close()

	var resp PingResponse
	if err := client.Call("HandshakeService.Ping", PingRequest{}, &resp); err != nil {
		t.Fatalf("HandshakeService.Ping: %v", err)
	}
	if resp.Version != rpcVersion {
		t.Errorf("PingResponse.Version = %q, erwartet %q", resp.Version, rpcVersion)
	}
	if resp.Version != "1.0" {
		t.Errorf("Basis-Version muss \"1.0\" sein, war %q", resp.Version)
	}

	// Zweiter Service muss erreichbar sein (Vertrag: DescribeContributions).
	var reply DescribeContributionsReply
	if err := client.Call("ContributionService.DescribeContributions", DescribeContributionsRequest{}, &reply); err != nil {
		t.Fatalf("ContributionService.DescribeContributions: %v", err)
	}
	if len(reply.Contributions) == 0 {
		t.Error("DescribeContributions lieferte keine Contributions")
	}
	if reply.ContributionVersion == "" {
		t.Error("ContributionVersion ist leer")
	}
}

// TestHello_FailsClosedWithoutEnv prüft, dass das Plugin ohne die
// benoetigten Env-Variablen nicht startet (kein Default, kein Ready).
func TestHello_FailsClosedWithoutEnv(t *testing.T) {
	ca := newTestCA(t)

	cases := []struct {
		name                      string
		withCert, withKey, withCA bool
	}{
		{"ohne alles", false, false, false},
		{"ohne cert", false, true, true},
		{"ohne key", true, false, true},
		{"ohne ca", true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ca.setEnv(t, tc.withCert, tc.withKey, tc.withCA)

			// run() wird direkt aufgerufen und muss einen Fehler liefern,
			// BEVOR ein Listener entsteht oder eine READY-Zeile gedruckt wird.
			if err := run(); err == nil {
				t.Fatal("run() lieferte kein Fehler-Ergebnis, obwohl Env fehlt")
			}
		})
	}
}

// TestHello_HandshakeWithoutClientCert verifies fail-closed enforcement of
// mutual TLS: a client without a certificate must be rejected by the handler.
func TestHello_HandshakeWithoutClientCert(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	conn, err := tls.Dial("tcp", addr, &tls.Config{
		RootCAs:    ca.pool,
		ServerName: "hello-plugin",
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		// Server darf den Handshake ohne Client-Cert schon beim Dial abbrechen.
		if want := "certificate"; !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Logf("Dial ohne Client-Zertifikat abgelehnt: %v", err)
		}
		return
	}
	defer conn.Close()
	// Falls der Handshake dennoch durchging, muss der erste RPC scheitern.
	client := rpc.NewClient(conn)
	defer client.Close()
	var resp PingResponse
	if err := client.Call("HandshakeService.Ping", PingRequest{}, &resp); err == nil {
		t.Fatal("Ping ohne Client-Zertifikat haette fehlschlagen muessen")
	} else if err != io.EOF && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "closed") {
		t.Logf("Ping ohne Client-Zertifikat erwartungsgemaess abgelehnt: %v", err)
	}
}
