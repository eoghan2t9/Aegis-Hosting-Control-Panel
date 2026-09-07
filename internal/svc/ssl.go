package svc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"aegis/internal/config"
	"aegis/internal/store"
)

// SSL issues and manages TLS certificates: Let's Encrypt (HTTP-01 webroot or
// DNS-01 via a provider plugin) and self-signed fallbacks.
type SSL struct {
	Cfg   *config.Config
	Store *store.Store
	Web   *WebServer
	DNS   *DNS
}

func NewSSL(cfg *config.Config, st *store.Store, web *WebServer, dnsSvc *DNS) *SSL {
	return &SSL{Cfg: cfg, Store: st, Web: web, DNS: dnsSvc}
}

// acmeUser implements registration.User for lego.
type acmeUser struct {
	email        string
	key          *ecdsa.PrivateKey
	registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// Issue obtains a certificate for a domain. challenge is "http" (webroot) or
// "dns" (provider DNS-01). On success the domain and order are updated and the
// web server config is regenerated with TLS enabled.
func (s *SSL) Issue(ctx context.Context, domainID int64, challenge string) (*store.SSLOrder, error) {
	dom, err := s.Store.GetDomain(ctx, domainID)
	if err != nil {
		return nil, err
	}
	user, err := s.Store.GetUserByID(ctx, dom.UserID)
	if err != nil {
		return nil, err
	}
	if challenge != "http" && challenge != "dns" {
		return nil, errors.New("challenge must be 'http' or 'dns'")
	}

	order := &store.SSLOrder{
		DomainID:  dom.ID,
		Status:    "pending",
		Provider:  "letsencrypt",
		Challenge: challenge,
	}
	if err := s.Store.CreateSSLOrder(ctx, order); err != nil {
		return nil, err
	}

	slog.Info("ssl: issuing certificate", "domain", dom.Domain, "challenge", challenge)
	certRes, err := s.obtain(ctx, dom, challenge)
	if err != nil {
		order.Status = "failed"
		order.Error = err.Error()
		_ = s.Store.UpdateSSLOrder(ctx, order)
		return order, err
	}

	// Persist certs.
	certDir := filepath.Join(s.Cfg.CertDir, dom.Domain)
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(certDir, "fullchain.pem")
	keyPath := filepath.Join(certDir, "privkey.pem")
	if err := os.WriteFile(certPath, certRes.Certificate, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, certRes.PrivateKey, 0o600); err != nil {
		return nil, err
	}

	expires := certNotAfter(certRes.Certificate)
	order.Status = "issued"
	order.CertPath = certPath
	order.KeyPath = keyPath
	order.ExpiresAt = &expires
	order.Error = ""
	if err := s.Store.UpdateSSLOrder(ctx, order); err != nil {
		return nil, err
	}

	// Update the domain and re-apply web config with TLS.
	dom.SSLEnabled = true
	dom.SSLCertPath = certPath
	dom.SSLKeyPath = keyPath
	dom.SSLProvider = "letsencrypt"
	if err := s.Store.UpdateDomain(ctx, dom); err != nil {
		return nil, err
	}
	aliases, _ := s.Store.ListAliases(ctx, dom.ID)
	if err := s.Web.Apply(dom, aliases, user.Username); err != nil {
		slog.Warn("ssl: cert issued but web config apply failed", "domain", dom.Domain, "err", err)
	}
	return order, nil
}

// obtain drives the ACME flow.
func (s *SSL) obtain(ctx context.Context, dom *store.Domain, challenge string) (*certificate.Resource, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	email := s.Cfg.PublicHost
	if email == "" {
		email = "admin@" + dom.Domain
	}
	acct := &acmeUser{email: email, key: key}
	cfg := lego.NewConfig(acct)
	if s.Cfg.LetsEncryptStaging {
		cfg.CADirURL = lego.LEDirectoryStaging
	} else {
		cfg.CADirURL = lego.LEDirectoryProduction
	}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssl: client: %w", err)
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, fmt.Errorf("ssl: register: %w", err)
	}
	acct.registration = reg

	switch challenge {
	case "http":
		if err := client.Challenge.SetHTTP01Provider(webrootProvider{root: dom.DocumentRoot}); err != nil {
			return nil, err
		}
	case "dns":
		prov, err := s.DNS.cloudflareProvider()
		if err != nil {
			return nil, err
		}
		cfp, ok := prov.(*cloudflareProvider)
		if !ok {
			return nil, errors.New("ssl: dns-01 requires the cloudflare provider")
		}
		if err := client.Challenge.SetDNS01Provider(cfDNS01Provider{p: cfp, ctx: ctx}); err != nil {
			return nil, err
		}
	}

	req := certificate.ObtainRequest{
		Domains: []string{dom.Domain},
		Bundle:  true,
	}
	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, fmt.Errorf("ssl: acme obtain: %w", err)
	}
	return res, nil
}

// webrootProvider writes the ACME token into the site document root so the
// active web server answers the challenge.
type webrootProvider struct{ root string }

func (w webrootProvider) Present(domain, token, keyAuth string) error {
	dir := filepath.Join(w.root, ".well-known", "acme-challenge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, token), []byte(keyAuth), 0o644)
}

func (w webrootProvider) CleanUp(domain, token, keyAuth string) error {
	_ = os.Remove(filepath.Join(w.root, ".well-known", "acme-challenge", token))
	return nil
}

// cfDNS01Provider answers DNS-01 challenges via the Cloudflare API.
type cfDNS01Provider struct {
	p   *cloudflareProvider
	ctx context.Context
}

func (c cfDNS01Provider) Present(domain, token, keyAuth string) error {
	zoneID, err := c.p.EnsureZone(c.ctx, domain)
	if err != nil {
		return err
	}
	name := "_acme-challenge"
	rec := map[string]interface{}{"type": "TXT", "name": name, "content": keyAuth, "ttl": 120}
	var out interface{}
	return c.p.do(c.ctx, "POST", "/zones/"+zoneID+"/dns_records", rec, &out)
}

func (c cfDNS01Provider) CleanUp(domain, token, keyAuth string) error {
	zoneID, err := c.p.EnsureZone(c.ctx, domain)
	if err != nil {
		return err
	}
	var recs []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := c.p.do(c.ctx, "GET", "/zones/"+zoneID+"/dns_records?type=TXT&name=_acme-challenge."+domain, nil, &recs); err != nil {
		return err
	}
	for _, r := range recs {
		if r.Content == keyAuth {
			_ = c.p.do(c.ctx, "DELETE", "/zones/"+zoneID+"/dns_records/"+r.ID, nil, nil)
		}
	}
	return nil
}

// SelfSigned generates an ECDSA self-signed certificate valid 1 year.
func (s *SSL) SelfSigned(ctx context.Context, domainID int64) (*store.SSLOrder, error) {
	dom, err := s.Store.GetDomain(ctx, domainID)
	if err != nil {
		return nil, err
	}
	order := &store.SSLOrder{DomainID: dom.ID, Status: "pending", Provider: "self-signed", Challenge: "self"}
	if err := s.Store.CreateSSLOrder(ctx, order); err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dom.Domain, Organization: []string{"Aegis"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dom.Domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certDir := filepath.Join(s.Cfg.CertDir, dom.Domain)
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(certDir, "fullchain.pem")
	keyPath := filepath.Join(certDir, "privkey.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}

	expires := tmpl.NotAfter
	order.Status = "issued"
	order.CertPath = certPath
	order.KeyPath = keyPath
	order.ExpiresAt = &expires
	_ = s.Store.UpdateSSLOrder(ctx, order)

	dom.SSLEnabled = true
	dom.SSLCertPath = certPath
	dom.SSLKeyPath = keyPath
	dom.SSLProvider = "self-signed"
	_ = s.Store.UpdateDomain(ctx, dom)
	user, _ := s.Store.GetUserByID(ctx, dom.UserID)
	aliases, _ := s.Store.ListAliases(ctx, dom.ID)
	if user != nil {
		_ = s.Web.Apply(dom, aliases, user.Username)
	}
	return order, nil
}

// AutoRenew loops forever, renewing certificates that expire within 30 days.
// Run in a goroutine by the server.
func (s *SSL) AutoRenew(ctx context.Context) {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.renewExpiring(ctx)
		}
	}
}

func (s *SSL) renewExpiring(ctx context.Context) {
	orders, err := s.Store.Renewables(ctx, 30)
	if err != nil {
		slog.Error("ssl: renew scan failed", "err", err)
		return
	}
	for _, o := range orders {
		if o.Provider != "letsencrypt" {
			continue
		}
		dom, err := s.Store.GetDomain(ctx, o.DomainID)
		if err != nil {
			continue
		}
		slog.Info("ssl: auto-renewing", "domain", dom.Domain)
		o.Status = "renewing"
		_ = s.Store.UpdateSSLOrder(ctx, o)
		if _, err := s.Issue(ctx, dom.ID, o.Challenge); err != nil {
			slog.Error("ssl: auto-renew failed", "domain", dom.Domain, "err", err)
		}
	}
}

// certNotAfter extracts the expiry from a PEM chain.
func certNotAfter(pemData []byte) time.Time {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return time.Time{}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return cert.NotAfter
}

// CertInfo returns parsed certificate metadata for a path (dashboard display).
func CertInfo(certPath string) (map[string]interface{}, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"subject":    cert.Subject.CommonName,
		"issuer":     cert.Issuer.CommonName,
		"not_before": cert.NotBefore.Format(time.RFC3339),
		"not_after":  cert.NotAfter.Format(time.RFC3339),
		"dns_names":  cert.DNSNames,
	}
	if ips := make([]string, 0, len(cert.IPAddresses)); len(cert.IPAddresses) > 0 {
		for _, ip := range cert.IPAddresses {
			ips = append(ips, ip.String())
		}
		out["ip_addresses"] = ips
	}
	return out, nil
}
