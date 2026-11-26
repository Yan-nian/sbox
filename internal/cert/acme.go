package cert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/registration"
)

// ACMEOptions configures ACME certificate issuance via HTTP-01 or DNS-01.
type ACMEOptions struct {
	CertDir     string
	AccountDir  string
	Email       string
	Domains     []string
	Challenge   string            // "http01" | "dns01"
	HTTPPort    string            // default "80" for http01
	DNSProvider string            // "cloudflare" | "alidns" | "dnspod"
	DNSCreds    map[string]string // cloudflare: api_token (or email+api_key); alidns: access_key+secret_key; dnspod: api_key (id,token)
	Staging     bool              // Let's Encrypt staging URL when true
}

type acmeUser struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string {
	return u.Email
}

func (u *acmeUser) GetRegistration() *registration.Resource {
	return u.Registration
}

func (u *acmeUser) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

type ctxRoundTripper struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t *ctxRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req.Clone(t.ctx))
}

// IssueACME obtains or renews a certificate using Let's Encrypt (production or staging)
// via HTTP-01 or targeted DNS-01 providers, persists the ACME account under opts.AccountDir,
// and writes fullchain.pem & privkey.pem atomically into opts.CertDir.
func IssueACME(ctx context.Context, opts ACMEOptions) (*CertInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cert: acme cancelled: %w", err)
	}

	normalized, dnsProvider, err := validateACMEOptions(opts)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(normalized.CertDir, 0o700); err != nil {
		return nil, fmt.Errorf("cert: create cert dir: %w", err)
	}

	accountScopeDir := acmeAccountScopeDir(normalized.AccountDir, normalized.Email, normalized.Staging)
	user, err := loadOrCreateACMEAccount(accountScopeDir, normalized.Email)
	if err != nil {
		return nil, err
	}

	cfg := lego.NewConfig(user)
	if normalized.Staging {
		cfg.CADirURL = lego.LEDirectoryStaging
	} else {
		cfg.CADirURL = lego.LEDirectoryProduction
	}
	cfg.Certificate.KeyType = certcrypto.EC256
	if cfg.HTTPClient != nil {
		cfg.HTTPClient.Transport = &ctxRoundTripper{
			ctx:  ctx,
			base: cfg.HTTPClient.Transport,
		}
	}

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("cert: init acme client: %w", err)
	}

	if user.Registration == nil {
		reg, regErr := client.Registration.ResolveAccountByKey()
		if regErr != nil {
			reg, regErr = client.Registration.Register(registration.RegisterOptions{
				TermsOfServiceAgreed: true,
			})
			if regErr != nil {
				return nil, fmt.Errorf("cert: register acme account: %w", regErr)
			}
		}
		user.Registration = reg
		if err := saveACMERegistration(accountScopeDir, reg); err != nil {
			return nil, fmt.Errorf("cert: save acme registration: %w", err)
		}
	}

	switch normalized.Challenge {
	case "http01":
		if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", normalized.HTTPPort)); err != nil {
			return nil, fmt.Errorf("cert: set http-01 provider: %w", err)
		}
	case "dns01":
		if err := client.Challenge.SetDNS01Provider(dnsProvider); err != nil {
			return nil, fmt.Errorf("cert: set dns-01 provider: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cert: acme cancelled before obtain: %w", err)
	}

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: normalized.Domains,
		Bundle:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("cert: obtain acme certificate: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cert: acme cancelled after obtain: %w", err)
	}

	certPath := filepath.Join(normalized.CertDir, fullchainFileName)
	keyPath := filepath.Join(normalized.CertDir, privkeyFileName)

	if err := writeFileAtomic(certPath, res.Certificate, 0o644); err != nil {
		return nil, fmt.Errorf("cert: write %s: %w", fullchainFileName, err)
	}
	if err := writeFileAtomic(keyPath, res.PrivateKey, 0o600); err != nil {
		return nil, fmt.Errorf("cert: write %s: %w", privkeyFileName, err)
	}

	return InspectCertFiles(certPath, keyPath)
}

func validateACMEOptions(opts ACMEOptions) (ACMEOptions, challenge.Provider, error) {
	norm := opts
	norm.CertDir = strings.TrimSpace(opts.CertDir)
	if norm.CertDir == "" {
		return norm, nil, errors.New("cert: cert_dir is required")
	}

	norm.AccountDir = strings.TrimSpace(opts.AccountDir)
	if norm.AccountDir == "" {
		norm.AccountDir = filepath.Join(norm.CertDir, ".acme-account")
	}

	norm.Email = strings.TrimSpace(opts.Email)
	if norm.Email == "" || !strings.Contains(norm.Email, "@") {
		return norm, nil, errors.New("cert: valid acme email is required")
	}

	norm.Domains = normalizeDomains(opts.Domains)
	if len(norm.Domains) == 0 {
		return norm, nil, errors.New("cert: at least one domain is required")
	}

	norm.Challenge = strings.ToLower(strings.TrimSpace(opts.Challenge))
	switch norm.Challenge {
	case "http01", "http-01":
		norm.Challenge = "http01"
		port := strings.TrimSpace(opts.HTTPPort)
		if port == "" {
			port = "80"
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return norm, nil, fmt.Errorf("cert: invalid http01 port %q", port)
		}
		norm.HTTPPort = port
		return norm, nil, nil

	case "dns01", "dns-01":
		norm.Challenge = "dns01"
		norm.DNSProvider = strings.ToLower(strings.TrimSpace(opts.DNSProvider))
		provider, err := buildDNSProvider(norm.DNSProvider, opts.DNSCreds)
		if err != nil {
			return norm, nil, err
		}
		return norm, provider, nil

	default:
		return norm, nil, fmt.Errorf("cert: unsupported acme challenge %q (must be http01 or dns01)", opts.Challenge)
	}
}

func buildDNSProvider(provider string, creds map[string]string) (challenge.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "cloudflare":
		cfg := cloudflare.NewDefaultConfig()
		apiToken := credLookup(creds, "api_token", "dns_api_token", "token", "cloudflare_dns_api_token", "cf_dns_api_token")
		zoneToken := credLookup(creds, "zone_api_token", "zone_token", "cloudflare_zone_api_token")
		email := credLookup(creds, "email", "auth_email", "cloudflare_email", "cf_api_email")
		apiKey := credLookup(creds, "api_key", "auth_key", "cloudflare_api_key", "cf_api_key")

		if apiToken != "" {
			cfg.AuthToken = apiToken
			if zoneToken != "" {
				cfg.ZoneToken = zoneToken
			}
		} else if email != "" && apiKey != "" {
			cfg.AuthEmail = email
			cfg.AuthKey = apiKey
		} else {
			return nil, errors.New("cert: cloudflare requires api_token (or email and api_key)")
		}

		p, err := cloudflare.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("cert: configure cloudflare dns provider: %w", err)
		}
		return p, nil

	case "alidns":
		cfg := alidns.NewDefaultConfig()
		accessKey := credLookup(creds, "access_key", "api_key", "access_key_id", "alicloud_access_key")
		secretKey := credLookup(creds, "secret_key", "access_key_secret", "alicloud_secret_key")
		securityToken := credLookup(creds, "security_token", "alicloud_security_token")
		regionID := credLookup(creds, "region_id", "alicloud_region_id")
		ramRole := credLookup(creds, "ram_role", "alicloud_ram_role")

		if ramRole != "" {
			cfg.RAMRole = ramRole
		} else if accessKey != "" && secretKey != "" {
			cfg.APIKey = accessKey
			cfg.SecretKey = secretKey
			cfg.SecurityToken = securityToken
		} else {
			return nil, errors.New("cert: alidns requires access_key and secret_key")
		}
		if regionID != "" {
			cfg.RegionID = regionID
		}

		p, err := alidns.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("cert: configure alidns provider: %w", err)
		}
		return p, nil

	case "dnspod":
		cfg := dnspod.NewDefaultConfig()
		apiKey := credLookup(creds, "api_key", "login_token", "dnspod_api_key")
		if apiKey == "" {
			id := credLookup(creds, "id", "token_id")
			token := credLookup(creds, "token", "api_token")
			if id != "" && token != "" {
				apiKey = id + "," + token
			}
		}
		if apiKey == "" {
			return nil, errors.New("cert: dnspod requires api_key (id,token)")
		}
		cfg.LoginToken = apiKey

		p, err := dnspod.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("cert: configure dnspod provider: %w", err)
		}
		return p, nil

	default:
		return nil, fmt.Errorf("cert: unsupported dns provider %q (supported: cloudflare, alidns, dnspod)", provider)
	}
}

func credLookup(creds map[string]string, keys ...string) string {
	if len(creds) == 0 {
		return ""
	}
	for _, want := range keys {
		if v, ok := creds[want]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	// Case-insensitive fallback lookup.
	lowered := make(map[string]string, len(creds))
	for k, v := range creds {
		lowered[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	for _, want := range keys {
		if v := lowered[strings.ToLower(want)]; v != "" {
			return v
		}
	}
	return ""
}

func acmeAccountScopeDir(accountDir, email string, staging bool) string {
	envName := "production"
	if staging {
		envName = "staging"
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return filepath.Join(accountDir, envName, hex.EncodeToString(sum[:8]))
}

func loadOrCreateACMEAccount(scopeDir, email string) (*acmeUser, error) {
	if err := os.MkdirAll(scopeDir, 0o700); err != nil {
		return nil, fmt.Errorf("cert: create acme account dir: %w", err)
	}

	keyPath := filepath.Join(scopeDir, "account.key")
	regPath := filepath.Join(scopeDir, "registration.json")

	var privKey crypto.PrivateKey
	if keyData, err := os.ReadFile(keyPath); err == nil {
		privKey, err = certcrypto.ParsePEMPrivateKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("cert: parse existing acme account key: %w", err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		ecKey, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if genErr != nil {
			return nil, fmt.Errorf("cert: generate acme account key: %w", genErr)
		}
		der, marshalErr := x509.MarshalPKCS8PrivateKey(ecKey)
		if marshalErr != nil {
			return nil, fmt.Errorf("cert: marshal acme account key: %w", marshalErr)
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		})
		if writeErr := writeFileAtomic(keyPath, pemBytes, 0o600); writeErr != nil {
			return nil, fmt.Errorf("cert: save acme account key: %w", writeErr)
		}
		privKey = ecKey
	} else {
		return nil, fmt.Errorf("cert: read acme account key: %w", err)
	}

	var reg *registration.Resource
	if regData, err := os.ReadFile(regPath); err == nil {
		var parsed registration.Resource
		if unmarshalErr := json.Unmarshal(regData, &parsed); unmarshalErr == nil && parsed.URI != "" {
			reg = &parsed
		}
	}

	return &acmeUser{
		Email:        email,
		Registration: reg,
		key:          privKey,
	}, nil
}

func saveACMERegistration(scopeDir string, reg *registration.Resource) error {
	if reg == nil {
		return nil
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(scopeDir, "registration.json"), data, 0o600)
}
