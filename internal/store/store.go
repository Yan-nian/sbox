package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

// ErrNotFound is returned when a requested database record does not exist.
var ErrNotFound = errors.New("record not found")

// TrafficDeltaInput represents an incremental traffic delta for a member/inbound pair.
type TrafficDeltaInput struct {
	InboundTag string
	MemberName string
	Upload     int64
	Download   int64
}

// Store wraps the SQLite database connection and provides domain CRUD operations.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens or creates the SQLite database at dbPath, enables WAL mode, and runs all migrations.
func Open(dbPath string) (*Store, error) {
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
			return nil, fmt.Errorf("create db parent dir: %w", err)
		}
	}

	dsn := dbPath
	if dbPath != ":memory:" && !strings.Contains(dbPath, "?") {
		dsn = dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Serialize writes through a single pooled connection to avoid SQLITE_BUSY under concurrent goroutines.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA synchronous = NORMAL;",
	}
	if dbPath != ":memory:" {
		pragmas = append([]string{"PRAGMA journal_mode = WAL;"}, pragmas...)
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("exec %s: %w", p, err)
		}
	}

	s := &Store{db: db, path: dbPath}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return s, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying *sql.DB handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Path returns the configured database path.
func (s *Store) Path() string {
	return s.path
}

// Migrate applies all pending SQLite schema migrations idempotently.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range model.Migrations {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", m.Version, err)
		}
		if exists > 0 {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.Version, m.Name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// TableNames returns all user table names in SQLite (excluding sqlite_* internal tables).
func (s *Store) TableNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// --- Settings CRUD ---

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) GetSettingDefault(ctx context.Context, key, def string) (string, error) {
	val, ok, err := s.GetSetting(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return def, nil
	}
	return val, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	return err
}

func (s *Store) SetSettings(ctx context.Context, kv map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for k, v := range kv {
		if _, err := stmt.ExecContext(ctx, k, v); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

func (s *Store) GetAllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// NormalizeBasePath ensures a base path starts with "/" and has no trailing "/" (unless empty or "/").
func NormalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// RandomHex generates n random bytes encoded as lowercase hex.
func RandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// EnsureBootstrapDefaults populates essential first-boot settings (random secret Web Base Path,
// JWT secret, instance ID, listen port, log level) if not already present.
func (s *Store) EnsureBootstrapDefaults(ctx context.Context, defaultPort int) (basePath, instanceID, jwtSecret string, err error) {
	if defaultPort <= 0 {
		defaultPort = 18080
	}

	all, err := s.GetAllSettings(ctx)
	if err != nil {
		return "", "", "", err
	}

	toWrite := make(map[string]string)

	basePath = NormalizeBasePath(all[model.SettingWebBasePath])
	if basePath == "" || basePath == "/" {
		suffix, err := RandomHex(4)
		if err != nil {
			return "", "", "", fmt.Errorf("generate random base path: %w", err)
		}
		basePath = "/sbox-" + suffix
		toWrite[model.SettingWebBasePath] = basePath
	}

	instanceID = strings.TrimSpace(all[model.SettingInstanceID])
	if instanceID == "" {
		suffix, err := RandomHex(3)
		if err != nil {
			return "", "", "", fmt.Errorf("generate instance id: %w", err)
		}
		host, _ := os.Hostname()
		host = sanitizeInstanceSegment(host)
		if host != "" {
			instanceID = fmt.Sprintf("%s-%s", host, suffix)
		} else {
			instanceID = "sbox-" + suffix
		}
		toWrite[model.SettingInstanceID] = instanceID
	}

	jwtSecret = strings.TrimSpace(all[model.SettingJWTSecret])
	if jwtSecret == "" {
		jwtSecret, err = RandomHex(32)
		if err != nil {
			return "", "", "", fmt.Errorf("generate jwt secret: %w", err)
		}
		toWrite[model.SettingJWTSecret] = jwtSecret
	}

	if _, ok := all[model.SettingListenPort]; !ok {
		toWrite[model.SettingListenPort] = strconv.Itoa(defaultPort)
	}
	if _, ok := all[model.SettingLogLevel]; !ok {
		toWrite[model.SettingLogLevel] = "info"
	}
	if _, ok := all[model.SettingInitialized]; !ok {
		toWrite[model.SettingInitialized] = "false"
	}
	if _, ok := all[model.SettingTOTPEnabled]; !ok {
		toWrite[model.SettingTOTPEnabled] = "false"
	}
	if _, ok := all[model.SettingPanelTLSCertID]; !ok {
		toWrite[model.SettingPanelTLSCertID] = "0"
	}

	if len(toWrite) > 0 {
		if err := s.SetSettings(ctx, toWrite); err != nil {
			return "", "", "", err
		}
	}
	return basePath, instanceID, jwtSecret, nil
}

func sanitizeInstanceSegment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == '_' || r == '.' || r == ' ' {
			b.WriteByte('-')
		}
		if b.Len() >= 20 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// GetPanelSettings aggregates public panel settings from the key-value settings table.
func (s *Store) GetPanelSettings(ctx context.Context) (model.PanelSettings, error) {
	all, err := s.GetAllSettings(ctx)
	if err != nil {
		return model.PanelSettings{}, err
	}
	port, _ := strconv.Atoi(all[model.SettingListenPort])
	if port <= 0 {
		port = 18080
	}
	panelCertID, _ := strconv.ParseInt(all[model.SettingPanelTLSCertID], 10, 64)
	if panelCertID < 0 {
		panelCertID = 0
	}
	logLevel := all[model.SettingLogLevel]
	if logLevel == "" {
		logLevel = "info"
	}
	return model.PanelSettings{
		InstanceID:  all[model.SettingInstanceID],
		WebBasePath: NormalizeBasePath(all[model.SettingWebBasePath]),
		ListenPort:  port,
		PublicIPv4:  all[model.SettingPublicIPv4],
		PublicIPv6:  all[model.SettingPublicIPv6],
		GithubProxy: all[model.SettingGithubProxy],
		LogLevel:    logLevel,
		TOTPEnabled: all[model.SettingTOTPEnabled] == "true",
		PanelCertID: panelCertID,
	}, nil
}

// UpdatePanelSettings persists editable PanelSettings fields atomically.
func (s *Store) UpdatePanelSettings(ctx context.Context, ps model.PanelSettings) error {
	updates := map[string]string{
		model.SettingPublicIPv4:     strings.TrimSpace(ps.PublicIPv4),
		model.SettingPublicIPv6:     strings.TrimSpace(ps.PublicIPv6),
		model.SettingGithubProxy:    strings.TrimSpace(ps.GithubProxy),
		model.SettingPanelTLSCertID: strconv.FormatInt(maxInt64(ps.PanelCertID, 0), 10),
	}
	if id := strings.TrimSpace(ps.InstanceID); id != "" {
		updates[model.SettingInstanceID] = id
	}
	if bp := NormalizeBasePath(ps.WebBasePath); bp != "" && bp != "/" {
		updates[model.SettingWebBasePath] = bp
	}
	if ps.ListenPort > 0 && ps.ListenPort <= 65535 {
		updates[model.SettingListenPort] = strconv.Itoa(ps.ListenPort)
	}
	if ll := strings.TrimSpace(ps.LogLevel); ll != "" {
		updates[model.SettingLogLevel] = ll
	}
	return s.SetSettings(ctx, updates)
}

// --- Certificates CRUD ---

func marshalDomains(domains []string) (primary string, raw string) {
	cleaned := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d != "" {
			cleaned = append(cleaned, d)
		}
	}
	if len(cleaned) > 0 {
		primary = cleaned[0]
	}
	b, _ := json.Marshal(cleaned)
	return primary, string(b)
}

func scanCertificate(scanner interface{ Scan(dest ...any) error }) (*model.Certificate, error) {
	var c model.Certificate
	var primaryDomain, domainsJSON string
	var notBefore, notAfter, expiresAt sql.NullTime
	var autoRenew, acmeStaging int
	err := scanner.Scan(
		&c.ID, &c.Name, &primaryDomain, &c.Type, &domainsJSON,
		&c.ACMEEmail, &c.ACMEHTTPPort, &acmeStaging,
		&c.DNSProvider, &c.DNSCredsJSON,
		&c.CertPath, &c.KeyPath, &c.Issuer, &c.Fingerprint,
		&notBefore, &notAfter, &expiresAt, &autoRenew, &c.LastError,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	c.AutoRenew = autoRenew != 0
	c.ACMEStaging = acmeStaging != 0
	c.HasDNSCreds = strings.TrimSpace(c.DNSCredsJSON) != "" && strings.TrimSpace(c.DNSCredsJSON) != "{}"
	_ = json.Unmarshal([]byte(domainsJSON), &c.Domains)
	if len(c.Domains) == 0 && primaryDomain != "" {
		c.Domains = []string{primaryDomain}
	}
	if c.Domains == nil {
		c.Domains = []string{}
	}
	if notBefore.Valid {
		t := notBefore.Time.UTC()
		c.NotBefore = &t
	}
	if notAfter.Valid {
		t := notAfter.Time.UTC()
		c.NotAfter = &t
	} else if expiresAt.Valid {
		t := expiresAt.Time.UTC()
		c.NotAfter = &t
	}
	return &c, nil
}

const certSelectColumns = `
	id, name, domain, cert_type, domains_json,
	acme_email, acme_http_port, acme_staging,
	dns_provider, dns_credentials_enc,
	cert_path, key_path, issuer, fingerprint,
	not_before, not_after, expires_at, auto_renew, last_renew_error,
	created_at, updated_at
`

func (s *Store) CreateCertificate(ctx context.Context, c *model.Certificate) error {
	primary, domainsJSON := marshalDomains(c.Domains)
	if c.ACMEHTTPPort == "" {
		c.ACMEHTTPPort = "80"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO certificates (
			name, domain, cert_type, domains_json,
			acme_email, acme_http_port, acme_staging,
			dns_provider, dns_credentials_enc,
			cert_path, key_path, issuer, fingerprint,
			not_before, not_after, expires_at, auto_renew, last_renew_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		c.Name, primary, c.Type, domainsJSON,
		c.ACMEEmail, c.ACMEHTTPPort, boolToInt(c.ACMEStaging),
		c.DNSProvider, c.DNSCredsJSON,
		c.CertPath, c.KeyPath, c.Issuer, c.Fingerprint,
		c.NotBefore, c.NotAfter, c.NotAfter, boolToInt(c.AutoRenew), c.LastError,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	saved, err := s.GetCertificate(ctx, id)
	if err == nil {
		*c = *saved
	}
	return nil
}

func (s *Store) GetCertificate(ctx context.Context, id int64) (*model.Certificate, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+certSelectColumns+` FROM certificates WHERE id = ?`, id)
	c, err := scanCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) ListCertificates(ctx context.Context) ([]model.Certificate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+certSelectColumns+` FROM certificates ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.Certificate, 0)
	for rows.Next() {
		c, err := scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *c)
	}
	return list, rows.Err()
}

func (s *Store) UpdateCertificate(ctx context.Context, c *model.Certificate) error {
	primary, domainsJSON := marshalDomains(c.Domains)
	if c.ACMEHTTPPort == "" {
		c.ACMEHTTPPort = "80"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE certificates SET
			name = ?, domain = ?, cert_type = ?, domains_json = ?,
			acme_email = ?, acme_http_port = ?, acme_staging = ?,
			dns_provider = ?, dns_credentials_enc = ?,
			cert_path = ?, key_path = ?, issuer = ?, fingerprint = ?,
			not_before = ?, not_after = ?, expires_at = ?, auto_renew = ?, last_renew_error = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		c.Name, primary, c.Type, domainsJSON,
		c.ACMEEmail, c.ACMEHTTPPort, boolToInt(c.ACMEStaging),
		c.DNSProvider, c.DNSCredsJSON,
		c.CertPath, c.KeyPath, c.Issuer, c.Fingerprint,
		c.NotBefore, c.NotAfter, c.NotAfter, boolToInt(c.AutoRenew), c.LastError,
		c.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if updated, err := s.GetCertificate(ctx, c.ID); err == nil {
		*c = *updated
	}
	return nil
}

func (s *Store) CountInboundsByCertificate(ctx context.Context, certID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM inbounds WHERE certificate_id = ?`, certID).Scan(&n)
	return n, err
}

func (s *Store) ClearInboundCertificateRefs(ctx context.Context, certID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE inbounds SET certificate_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE certificate_id = ?`, certID)
	return err
}

func (s *Store) DeleteCertificate(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM certificates WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Members CRUD ---

const memberSelectColumns = `
	id, name, enabled, uuid, password, ss2022_key, ss2022_key16, ss2022_key32,
	sub_token, upload_bytes, download_bytes, remark, created_at, updated_at
`

func scanMember(scanner interface{ Scan(dest ...any) error }) (*model.Member, error) {
	var m model.Member
	var enabled int
	var legacySSKey string
	err := scanner.Scan(
		&m.ID, &m.Name, &enabled, &m.UUID, &m.Password,
		&legacySSKey, &m.SS2022Key16, &m.SS2022Key32,
		&m.SubToken, &m.UploadBytes, &m.DownloadBytes, &m.Note,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	m.Enabled = enabled != 0
	if m.SS2022Key16 == "" && legacySSKey != "" {
		m.SS2022Key16 = legacySSKey
	}
	return &m, nil
}

func (s *Store) CreateMember(ctx context.Context, m *model.Member) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO members (name, enabled, uuid, password, ss2022_key, ss2022_key16, ss2022_key32, sub_token, upload_bytes, download_bytes, remark)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.Name, boolToInt(m.Enabled), m.UUID, m.Password, m.SS2022Key16, m.SS2022Key16, m.SS2022Key32, m.SubToken, m.UploadBytes, m.DownloadBytes, m.Note)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	m.ID = id
	if saved, err := s.GetMember(ctx, id); err == nil {
		*m = *saved
	}
	return nil
}

func (s *Store) GetMember(ctx context.Context, id int64) (*model.Member, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+memberSelectColumns+` FROM members WHERE id = ?`, id)
	m, err := scanMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) GetMemberByName(ctx context.Context, name string) (*model.Member, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+memberSelectColumns+` FROM members WHERE name = ?`, name)
	m, err := scanMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) GetMemberBySubToken(ctx context.Context, token string) (*model.Member, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+memberSelectColumns+` FROM members WHERE sub_token = ?`, token)
	m, err := scanMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) ListMembers(ctx context.Context) ([]model.Member, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memberSelectColumns+` FROM members ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.Member, 0)
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, rows.Err()
}

func (s *Store) UpdateMember(ctx context.Context, m *model.Member) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE members
		SET name = ?, enabled = ?, uuid = ?, password = ?, ss2022_key = ?, ss2022_key16 = ?, ss2022_key32 = ?, sub_token = ?, remark = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, m.Name, boolToInt(m.Enabled), m.UUID, m.Password, m.SS2022Key16, m.SS2022Key16, m.SS2022Key32, m.SubToken, m.Note, m.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetMember(ctx, m.ID); err == nil {
		*m = *saved
	}
	return nil
}

func (s *Store) ToggleMember(ctx context.Context, id int64) (*model.Member, error) {
	m, err := s.GetMember(ctx, id)
	if err != nil {
		return nil, err
	}
	m.Enabled = !m.Enabled
	if err := s.UpdateMember(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) DeleteMember(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM members WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Inbounds CRUD ---

const inboundSelectColumns = `
	id, name, tag, protocol, enabled, listen, listen_port, config_json,
	certificate_id, member_bind_mode, member_ids_json,
	external_host, external_port, external_entries_json,
	upload_bytes, download_bytes, created_at, updated_at
`

func scanInbound(scanner interface{ Scan(dest ...any) error }) (*model.Inbound, error) {
	var in model.Inbound
	var enabled int
	var certID sql.NullInt64
	var cfgJSON, memberIDsJSON, extraEntriesJSON string
	err := scanner.Scan(
		&in.ID, &in.Name, &in.Tag, &in.Protocol, &enabled,
		&in.Listen, &in.ListenPort, &cfgJSON,
		&certID, &in.MemberMode, &memberIDsJSON,
		&in.ExternalHost, &in.ExternalPort, &extraEntriesJSON,
		&in.UploadBytes, &in.DownloadBytes, &in.CreatedAt, &in.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	in.Enabled = enabled != 0
	if certID.Valid {
		v := certID.Int64
		in.CertificateID = &v
	}
	cfgJSON = strings.TrimSpace(cfgJSON)
	if cfgJSON == "" {
		cfgJSON = "{}"
	}
	in.Settings = json.RawMessage(cfgJSON)
	if in.MemberMode == "" {
		in.MemberMode = "all"
	}
	_ = json.Unmarshal([]byte(memberIDsJSON), &in.MemberIDs)
	if in.MemberIDs == nil {
		in.MemberIDs = []int64{}
	}
	_ = json.Unmarshal([]byte(extraEntriesJSON), &in.ExtraEntries)
	if in.ExtraEntries == nil {
		in.ExtraEntries = []model.ExtraEntry{}
	}
	return &in, nil
}

func normalizeInboundDefaults(in *model.Inbound) (cfgJSON, memberIDsJSON, extraEntriesJSON string) {
	if strings.TrimSpace(in.Listen) == "" {
		in.Listen = "::"
	}
	if len(in.Settings) == 0 || strings.TrimSpace(string(in.Settings)) == "" {
		in.Settings = json.RawMessage("{}")
	}
	cfgJSON = string(in.Settings)
	if in.MemberMode != "selected" {
		in.MemberMode = "all"
	}
	if in.MemberIDs == nil {
		in.MemberIDs = []int64{}
	}
	mb, _ := json.Marshal(in.MemberIDs)
	memberIDsJSON = string(mb)

	if in.ExtraEntries == nil {
		in.ExtraEntries = []model.ExtraEntry{}
	}
	eb, _ := json.Marshal(in.ExtraEntries)
	extraEntriesJSON = string(eb)
	return cfgJSON, memberIDsJSON, extraEntriesJSON
}

func (s *Store) CreateInbound(ctx context.Context, in *model.Inbound) error {
	cfgJSON, memberIDsJSON, extraEntriesJSON := normalizeInboundDefaults(in)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO inbounds (
			name, tag, protocol, enabled, listen, listen_port, config_json,
			certificate_id, member_bind_mode, member_ids_json,
			external_host, external_port, external_entries_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		in.Name, in.Tag, in.Protocol, boolToInt(in.Enabled), in.Listen, in.ListenPort, cfgJSON,
		in.CertificateID, in.MemberMode, memberIDsJSON,
		in.ExternalHost, in.ExternalPort, extraEntriesJSON,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	in.ID = id
	if saved, err := s.GetInbound(ctx, id); err == nil {
		*in = *saved
	}
	return nil
}

func (s *Store) GetInbound(ctx context.Context, id int64) (*model.Inbound, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+inboundSelectColumns+` FROM inbounds WHERE id = ?`, id)
	in, err := scanInbound(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func (s *Store) GetInboundByTag(ctx context.Context, tag string) (*model.Inbound, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+inboundSelectColumns+` FROM inbounds WHERE tag = ?`, tag)
	in, err := scanInbound(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func (s *Store) ListInbounds(ctx context.Context) ([]model.Inbound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+inboundSelectColumns+` FROM inbounds ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.Inbound, 0)
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *in)
	}
	return list, rows.Err()
}

func (s *Store) UpdateInbound(ctx context.Context, in *model.Inbound) error {
	cfgJSON, memberIDsJSON, extraEntriesJSON := normalizeInboundDefaults(in)
	res, err := s.db.ExecContext(ctx, `
		UPDATE inbounds SET
			name = ?, tag = ?, protocol = ?, enabled = ?, listen = ?, listen_port = ?, config_json = ?,
			certificate_id = ?, member_bind_mode = ?, member_ids_json = ?,
			external_host = ?, external_port = ?, external_entries_json = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		in.Name, in.Tag, in.Protocol, boolToInt(in.Enabled), in.Listen, in.ListenPort, cfgJSON,
		in.CertificateID, in.MemberMode, memberIDsJSON,
		in.ExternalHost, in.ExternalPort, extraEntriesJSON,
		in.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetInbound(ctx, in.ID); err == nil {
		*in = *saved
	}
	return nil
}

func (s *Store) ToggleInbound(ctx context.Context, id int64) (*model.Inbound, error) {
	in, err := s.GetInbound(ctx, id)
	if err != nil {
		return nil, err
	}
	in.Enabled = !in.Enabled
	if err := s.UpdateInbound(ctx, in); err != nil {
		return nil, err
	}
	return in, nil
}

func (s *Store) DeleteInbound(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM inbounds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Upstream Subscriptions, Extra Outbounds, Route Rules, Sync Targets ---

func (s *Store) CreateUpstreamSub(ctx context.Context, u *model.UpstreamSub) error {
	if u.CachedNodesJSON == "" {
		u.CachedNodesJSON = "[]"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO upstream_subs (name, url, enabled, update_cron, include_regex, exclude_regex, selector_tag, urltest_tag, cached_nodes_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, u.Name, u.URL, boolToInt(u.Enabled), u.UpdateCron, u.IncludeRegex, u.ExcludeRegex, u.SelectorTag, u.URLTestTag, u.CachedNodesJSON)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	u.ID = id
	return nil
}

func (s *Store) ListUpstreamSubs(ctx context.Context) ([]model.UpstreamSub, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, enabled, update_cron, include_regex, exclude_regex, selector_tag, urltest_tag, cached_nodes_json, last_fetch_status, last_fetch_error, last_fetched_at, created_at, updated_at
		FROM upstream_subs ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.UpstreamSub, 0)
	for rows.Next() {
		var u model.UpstreamSub
		var enabled int
		var fetched sql.NullTime
		if err := rows.Scan(&u.ID, &u.Name, &u.URL, &enabled, &u.UpdateCron, &u.IncludeRegex, &u.ExcludeRegex, &u.SelectorTag, &u.URLTestTag, &u.CachedNodesJSON, &u.LastFetchStatus, &u.LastFetchError, &fetched, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.Enabled = enabled != 0
		if fetched.Valid {
			t := fetched.Time
			u.LastFetchedAt = &t
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

func (s *Store) CreateOutboundExtra(ctx context.Context, o *model.OutboundExtra) error {
	if o.ConfigJSON == "" {
		o.ConfigJSON = "{}"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO outbounds_extra (name, tag, kind, enabled, config_json)
		VALUES (?, ?, ?, ?, ?)
	`, o.Name, o.Tag, o.Kind, boolToInt(o.Enabled), o.ConfigJSON)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	o.ID = id
	return nil
}

func (s *Store) ListOutboundsExtra(ctx context.Context) ([]model.OutboundExtra, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, tag, kind, enabled, config_json, created_at, updated_at
		FROM outbounds_extra ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.OutboundExtra, 0)
	for rows.Next() {
		var o model.OutboundExtra
		var enabled int
		if err := rows.Scan(&o.ID, &o.Name, &o.Tag, &o.Kind, &enabled, &o.ConfigJSON, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.Enabled = enabled != 0
		list = append(list, o)
	}
	return list, rows.Err()
}

func (s *Store) CreateRouteRule(ctx context.Context, r *model.RouteRule) error {
	if r.MatchJSON == "" {
		r.MatchJSON = "{}"
	}
	if r.PresetType == "" {
		r.PresetType = "custom"
	}
	if r.Action == "" {
		r.Action = "route"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO route_rules (name, priority, preset_type, match_json, action, outbound_tag, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, r.Name, r.Priority, r.PresetType, r.MatchJSON, r.Action, r.OutboundTag, boolToInt(r.Enabled))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

func (s *Store) ListRouteRules(ctx context.Context) ([]model.RouteRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, priority, preset_type, match_json, action, outbound_tag, enabled, created_at, updated_at
		FROM route_rules ORDER BY priority ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.RouteRule, 0)
	for rows.Next() {
		var r model.RouteRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &r.PresetType, &r.MatchJSON, &r.Action, &r.OutboundTag, &enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Store) CreateSyncTarget(ctx context.Context, t *model.SyncTarget) error {
	if t.SyncMode == "" {
		t.SyncMode = "independent"
	}
	if t.SubFormat == "" {
		t.SubFormat = "base64"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_targets (name, target_type, enabled, endpoint_url, auth_token_enc, target_ref, sync_mode, sub_format)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, t.Name, t.TargetType, boolToInt(t.Enabled), t.EndpointURL, t.AuthTokenEnc, t.TargetRef, t.SyncMode, t.SubFormat)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.ID = id
	return nil
}

func (s *Store) ListSyncTargets(ctx context.Context) ([]model.SyncTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, target_type, enabled, endpoint_url, auth_token_enc, target_ref, sync_mode, sub_format, last_sync_status, last_sync_http_code, last_sync_error, last_synced_at, created_at, updated_at
		FROM sync_targets ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.SyncTarget, 0)
	for rows.Next() {
		var t model.SyncTarget
		var enabled int
		var synced sql.NullTime
		if err := rows.Scan(&t.ID, &t.Name, &t.TargetType, &enabled, &t.EndpointURL, &t.AuthTokenEnc, &t.TargetRef, &t.SyncMode, &t.SubFormat, &t.LastSyncStatus, &t.LastSyncHTTPCode, &t.LastSyncError, &synced, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled != 0
		if synced.Valid {
			ts := synced.Time
			t.LastSyncedAt = &ts
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// --- Traffic Stats ---

// RecordTrafficDeltas upserts hourly and daily traffic buckets and increments member/inbound totals.
func (s *Store) RecordTrafficDeltas(ctx context.Context, now time.Time, deltas []TrafficDeltaInput) error {
	if len(deltas) == 0 {
		return nil
	}
	nowUTC := now.UTC()
	hourBucket := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), nowUTC.Hour(), 0, 0, 0, time.UTC)
	dayBucket := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	upsertStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO traffic_stats (member_id, inbound_id, member_name, inbound_tag, granularity, bucket_time, upload_bytes, download_bytes)
		VALUES (
			(SELECT id FROM members WHERE name = ? LIMIT 1),
			(SELECT id FROM inbounds WHERE tag = ? LIMIT 1),
			?, ?, ?, ?, ?, ?
		)
		ON CONFLICT(member_name, inbound_tag, granularity, bucket_time) DO UPDATE SET
			member_id = COALESCE(traffic_stats.member_id, excluded.member_id),
			inbound_id = COALESCE(traffic_stats.inbound_id, excluded.inbound_id),
			upload_bytes = traffic_stats.upload_bytes + excluded.upload_bytes,
			download_bytes = traffic_stats.download_bytes + excluded.download_bytes
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer upsertStmt.Close()

	memberUpd, err := tx.PrepareContext(ctx, `
		UPDATE members SET upload_bytes = upload_bytes + ?, download_bytes = download_bytes + ?, updated_at = CURRENT_TIMESTAMP
		WHERE name = ?
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer memberUpd.Close()

	inboundUpd, err := tx.PrepareContext(ctx, `
		UPDATE inbounds SET upload_bytes = upload_bytes + ?, download_bytes = download_bytes + ?, updated_at = CURRENT_TIMESTAMP
		WHERE tag = ?
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer inboundUpd.Close()

	for _, d := range deltas {
		if d.Upload <= 0 && d.Download <= 0 {
			continue
		}
		// Normalize ShadowTLS detour tags back to the parent inbound tag if present
		inboundTag := strings.TrimSuffix(d.InboundTag, "-detour")
		if _, err := upsertStmt.ExecContext(ctx, d.MemberName, inboundTag, d.MemberName, inboundTag, "hour", hourBucket, d.Upload, d.Download); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := upsertStmt.ExecContext(ctx, d.MemberName, inboundTag, d.MemberName, inboundTag, "day", dayBucket, d.Upload, d.Download); err != nil {
			_ = tx.Rollback()
			return err
		}
		if d.MemberName != "" {
			if _, err := memberUpd.ExecContext(ctx, d.Upload, d.Download, d.MemberName); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if inboundTag != "" {
			if _, err := inboundUpd.ExecContext(ctx, d.Upload, d.Download, inboundTag); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *Store) ListTrafficStats(ctx context.Context, granularity string, since time.Time, limit int) ([]model.TrafficStat, error) {
	if granularity == "" {
		granularity = "hour"
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, member_id, inbound_id, member_name, inbound_tag, granularity, bucket_time, upload_bytes, download_bytes
		FROM traffic_stats
		WHERE granularity = ? AND bucket_time >= ?
		ORDER BY bucket_time ASC
		LIMIT ?
	`, granularity, since.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.TrafficStat, 0)
	for rows.Next() {
		var ts model.TrafficStat
		var mID, iID sql.NullInt64
		if err := rows.Scan(&ts.ID, &mID, &iID, &ts.MemberName, &ts.InboundTag, &ts.Granularity, &ts.BucketTime, &ts.UploadBytes, &ts.DownloadBytes); err != nil {
			return nil, err
		}
		if mID.Valid {
			v := mID.Int64
			ts.MemberID = &v
		}
		if iID.Valid {
			v := iID.Int64
			ts.InboundID = &v
		}
		list = append(list, ts)
	}
	return list, rows.Err()
}

// GetTrafficStats queries aggregated time-series buckets (24h hourly, 7d/30d daily)
// along with per-member and per-inbound traffic breakdowns.
func (s *Store) GetTrafficStats(ctx context.Context, rangeKey string, memberID, inboundID int64) (model.TrafficStatsResponse, error) {
	now := time.Now().UTC()
	var granularity string
	var since time.Time

	switch strings.ToLower(strings.TrimSpace(rangeKey)) {
	case "7d":
		rangeKey = "7d"
		granularity = "day"
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	case "30d":
		rangeKey = "30d"
		granularity = "day"
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -29)
	default:
		rangeKey = "24h"
		granularity = "hour"
		since = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC).Add(-23 * time.Hour)
	}

	resp := model.TrafficStatsResponse{
		Range:     rangeKey,
		Series:    make([]model.TrafficSeriesPoint, 0),
		ByMember:  make([]model.MemberTrafficItem, 0),
		ByInbound: make([]model.InboundTrafficItem, 0),
	}

	// Build filtered time series query
	var where []string
	var args []any
	where = append(where, "granularity = ?", "bucket_time >= ?")
	args = append(args, granularity, since)
	if memberID > 0 {
		where = append(where, "member_id = ?")
		args = append(args, memberID)
	}
	if inboundID > 0 {
		where = append(where, "inbound_id = ?")
		args = append(args, inboundID)
	}

	seriesQuery := `
		SELECT bucket_time, COALESCE(SUM(upload_bytes), 0), COALESCE(SUM(download_bytes), 0)
		FROM traffic_stats
		WHERE ` + strings.Join(where, " AND ") + `
		GROUP BY bucket_time
		ORDER BY bucket_time ASC
	`
	rows, err := s.db.QueryContext(ctx, seriesQuery, args...)
	if err != nil {
		return resp, err
	}
	for rows.Next() {
		var bt time.Time
		var up, down int64
		if err := rows.Scan(&bt, &up, &down); err != nil {
			_ = rows.Close()
			return resp, err
		}
		resp.Series = append(resp.Series, model.TrafficSeriesPoint{
			BucketTime:    bt.UTC().Format(time.RFC3339),
			UploadBytes:   up,
			DownloadBytes: down,
		})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return resp, err
	}
	_ = rows.Close()

	// By member breakdown
	mQuery := `SELECT id, name, upload_bytes, download_bytes FROM members`
	var mArgs []any
	if memberID > 0 {
		mQuery += ` WHERE id = ?`
		mArgs = append(mArgs, memberID)
	}
	mQuery += ` ORDER BY (upload_bytes + download_bytes) DESC, id ASC`
	mRows, err := s.db.QueryContext(ctx, mQuery, mArgs...)
	if err != nil {
		return resp, err
	}
	for mRows.Next() {
		var item model.MemberTrafficItem
		if err := mRows.Scan(&item.ID, &item.Name, &item.UploadBytes, &item.DownloadBytes); err != nil {
			_ = mRows.Close()
			return resp, err
		}
		resp.ByMember = append(resp.ByMember, item)
	}
	_ = mRows.Close()

	// By inbound breakdown
	iQuery := `SELECT id, name, tag, protocol, upload_bytes, download_bytes FROM inbounds`
	var iArgs []any
	if inboundID > 0 {
		iQuery += ` WHERE id = ?`
		iArgs = append(iArgs, inboundID)
	}
	iQuery += ` ORDER BY (upload_bytes + download_bytes) DESC, id ASC`
	iRows, err := s.db.QueryContext(ctx, iQuery, iArgs...)
	if err != nil {
		return resp, err
	}
	for iRows.Next() {
		var item model.InboundTrafficItem
		if err := iRows.Scan(&item.ID, &item.Name, &item.Tag, &item.Protocol, &item.UploadBytes, &item.DownloadBytes); err != nil {
			_ = iRows.Close()
			return resp, err
		}
		resp.ByInbound = append(resp.ByInbound, item)
	}
	_ = iRows.Close()

	return resp, nil
}

// ResetTraffic resets cumulative and bucketed traffic counters for all, a member, or an inbound.
func (s *Store) ResetTraffic(ctx context.Context, scope string, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "all":
		if _, err := tx.ExecContext(ctx, `UPDATE members SET upload_bytes = 0, download_bytes = 0, updated_at = CURRENT_TIMESTAMP`); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbounds SET upload_bytes = 0, download_bytes = 0, updated_at = CURRENT_TIMESTAMP`); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_stats`); err != nil {
			_ = tx.Rollback()
			return err
		}
	case "member":
		if id <= 0 {
			_ = tx.Rollback()
			return errors.New("member id is required")
		}
		res, err := tx.ExecContext(ctx, `UPDATE members SET upload_bytes = 0, download_bytes = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			_ = tx.Rollback()
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_stats WHERE member_id = ? OR member_name = (SELECT name FROM members WHERE id = ?)`, id, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	case "inbound":
		if id <= 0 {
			_ = tx.Rollback()
			return errors.New("inbound id is required")
		}
		res, err := tx.ExecContext(ctx, `UPDATE inbounds SET upload_bytes = 0, download_bytes = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			_ = tx.Rollback()
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_stats WHERE inbound_id = ? OR inbound_tag = (SELECT tag FROM inbounds WHERE id = ?)`, id, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	default:
		_ = tx.Rollback()
		return errors.New("scope must be 'all', 'member', or 'inbound'")
	}

	return tx.Commit()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
