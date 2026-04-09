package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"atlas-runtime-go/internal/config"
)

type TLSAssets struct {
	CertPath string
	KeyPath  string
	Cert     tls.Certificate
}

// EnsureTLSAssets loads an existing built-in HTTPS certificate or generates a
// new self-signed certificate when none exists or when the current host names
// and IPs are no longer covered by the stored certificate.
func EnsureTLSAssets() (*TLSAssets, error) {
	if err := os.MkdirAll(config.TLSDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create tls dir: %w", err)
	}

	expectedDNS, expectedIPs := currentTLSSANs()
	certPath := config.TLSCertPath()
	keyPath := config.TLSKeyPath()

	if certOK(certPath, keyPath, expectedDNS, expectedIPs) {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load tls keypair: %w", err)
		}
		return &TLSAssets{CertPath: certPath, KeyPath: keyPath, Cert: cert}, nil
	}

	certPEM, keyPEM, err := generateSelfSignedCert(expectedDNS, expectedIPs)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("write tls cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write tls key: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("reload tls keypair: %w", err)
	}
	return &TLSAssets{CertPath: certPath, KeyPath: keyPath, Cert: cert}, nil
}

func certOK(certPath, keyPath string, expectedDNS []string, expectedIPs []net.IP) bool {
	if _, err := os.Stat(certPath); err != nil {
		return false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return false
	}
	for _, dnsName := range expectedDNS {
		if !containsString(cert.DNSNames, dnsName) {
			return false
		}
	}
	for _, ip := range expectedIPs {
		if !containsIP(cert.IPAddresses, ip) {
			return false
		}
	}
	return true
}

func currentTLSSANs() ([]string, []net.IP) {
	dnsSet := map[string]struct{}{
		"localhost": {},
	}
	ipSet := map[string]net.IP{
		net.IPv4(127, 0, 0, 1).String(): net.IPv4(127, 0, 0, 1),
		"::1":                           net.ParseIP("::1"),
	}

	if hostname, err := os.Hostname(); err == nil {
		hostname = strings.TrimSpace(strings.ToLower(hostname))
		if hostname != "" {
			dnsSet[hostname] = struct{}{}
			if !strings.Contains(hostname, ".") {
				dnsSet[hostname+".local"] = struct{}{}
			}
		}
	}

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
					continue
				}
				ipSet[ip.String()] = ip
			}
		}
	}

	dnsNames := make([]string, 0, len(dnsSet))
	for dnsName := range dnsSet {
		dnsNames = append(dnsNames, dnsName)
	}
	sort.Strings(dnsNames)

	ips := make([]net.IP, 0, len(ipSet))
	for _, ip := range ipSet {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool {
		return ips[i].String() < ips[j].String()
	})

	return dnsNames, ips
}

func generateSelfSignedCert(dnsNames []string, ips []net.IP) ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate tls key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate tls serial: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Project Atlas"},
			CommonName:   "Atlas Built-in HTTPS",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("create tls cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal tls key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func containsIP(values []net.IP, target net.IP) bool {
	for _, value := range values {
		if value.Equal(target) {
			return true
		}
	}
	return false
}
