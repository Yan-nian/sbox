package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

// ensureMilestone3Schema performs an idempotent, zero-data-loss upgrade from M1/M2 SQLite schemas
// to Milestone 3 multi-node support (`nodes` table, per-table `node_id`, per-node inbound tag &
// traffic_stats uniqueness, and local node id=1 seeding from existing `settings`).
func (s *Store) ensureMilestone3Schema(ctx context.Context) error {
	// 1. Always ensure `nodes` table exists.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			is_local INTEGER NOT NULL DEFAULT 0,
			token TEXT NOT NULL DEFAULT '',
			public_ipv4 TEXT NOT NULL DEFAULT '',
			public_ipv6 TEXT NOT NULL DEFAULT '',
			override_patch TEXT NOT NULL DEFAULT '',
			log_level TEXT NOT NULL DEFAULT 'info',
			github_proxy TEXT NOT NULL DEFAULT '',
			os TEXT NOT NULL DEFAULT '',
			arch TEXT NOT NULL DEFAULT '',
			hostname TEXT NOT NULL DEFAULT '',
			agent_version TEXT NOT NULL DEFAULT '',
			core_version TEXT NOT NULL DEFAULT '',
			data_dir TEXT NOT NULL DEFAULT '',
			last_seen_at DATETIME,
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_token_nonempty ON nodes(token) WHERE token != '';
	`); err != nil {
		return fmt.Errorf("create nodes table: %w", err)
	}

	// 2. Idempotent PRAGMA table_info column checks on all node-scoped tables.
	columnSpecs := []struct {
		table string
		col   string
		def   string
	}{
		{"certificates", "node_id", "INTEGER NOT NULL DEFAULT 1"},
		{"certificates", "cert_pem", "TEXT NOT NULL DEFAULT ''"},
		{"certificates", "key_pem", "TEXT NOT NULL DEFAULT ''"},
		{"inbounds", "node_id", "INTEGER NOT NULL DEFAULT 1"},
		{"outbounds_extra", "node_id", "INTEGER NOT NULL DEFAULT 1"},
		{"route_rules", "node_id", "INTEGER NOT NULL DEFAULT 1"},
		{"traffic_stats", "node_id", "INTEGER NOT NULL DEFAULT 1"},
		{"upstream_subs", "node_id", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, spec := range columnSpecs {
		if err := s.ensureColumn(ctx, spec.table, spec.col, spec.def); err != nil {
			return err
		}
	}

	// 3. Version 3 migration: rebuild `inbounds` (UNIQUE(node_id, tag)) and `traffic_stats`
	// (UNIQUE(node_id, member_name, inbound_tag, granularity, bucket_time)) preserving 100% of rows.
	var v3Applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = 3`).Scan(&v3Applied); err != nil {
		return fmt.Errorf("check migration 3: %w", err)
	}
	if v3Applied == 0 {
		// Disable foreign_keys outside transaction while rebuilding parent/child tables safely.
		if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF;`); err != nil {
			return fmt.Errorf("disable foreign_keys for m3 rebuild: %w", err)
		}
		defer func() {
			_, _ = s.db.ExecContext(context.Background(), `PRAGMA foreign_keys = ON;`)
		}()

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration 3: %w", err)
		}

		rebuildSQL := `
CREATE TABLE inbounds_m3_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id INTEGER NOT NULL DEFAULT 1,
	name TEXT NOT NULL,
	tag TEXT NOT NULL,
	protocol TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	listen TEXT NOT NULL DEFAULT '::',
	listen_port INTEGER NOT NULL,
	config_json TEXT NOT NULL DEFAULT '{}',
	certificate_id INTEGER REFERENCES certificates(id) ON DELETE SET NULL,
	member_bind_mode TEXT NOT NULL DEFAULT 'all',
	member_ids_json TEXT NOT NULL DEFAULT '[]',
	external_host TEXT NOT NULL DEFAULT '',
	external_port INTEGER NOT NULL DEFAULT 0,
	external_entries_json TEXT NOT NULL DEFAULT '[]',
	upload_bytes INTEGER NOT NULL DEFAULT 0,
	download_bytes INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(node_id, tag)
);

INSERT INTO inbounds_m3_new (
	id, node_id, name, tag, protocol, enabled, listen, listen_port, config_json,
	certificate_id, member_bind_mode, member_ids_json, external_host, external_port,
	external_entries_json, upload_bytes, download_bytes, created_at, updated_at
)
SELECT
	id, node_id, name, tag, protocol, enabled, listen, listen_port, config_json,
	certificate_id, member_bind_mode, member_ids_json, external_host, external_port,
	external_entries_json, upload_bytes, download_bytes, created_at, updated_at
FROM inbounds;

DROP TABLE inbounds;
ALTER TABLE inbounds_m3_new RENAME TO inbounds;

CREATE INDEX IF NOT EXISTS idx_inbounds_enabled ON inbounds(enabled);
CREATE INDEX IF NOT EXISTS idx_inbounds_certificate_id ON inbounds(certificate_id);
CREATE INDEX IF NOT EXISTS idx_inbounds_node_id ON inbounds(node_id);

CREATE TABLE traffic_stats_m3_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id INTEGER NOT NULL DEFAULT 1,
	member_id INTEGER REFERENCES members(id) ON DELETE CASCADE,
	inbound_id INTEGER REFERENCES inbounds(id) ON DELETE CASCADE,
	member_name TEXT NOT NULL DEFAULT '',
	inbound_tag TEXT NOT NULL DEFAULT '',
	granularity TEXT NOT NULL DEFAULT 'hour',
	bucket_time DATETIME NOT NULL,
	upload_bytes INTEGER NOT NULL DEFAULT 0,
	download_bytes INTEGER NOT NULL DEFAULT 0,
	UNIQUE(node_id, member_name, inbound_tag, granularity, bucket_time)
);

INSERT INTO traffic_stats_m3_new (
	id, node_id, member_id, inbound_id, member_name, inbound_tag,
	granularity, bucket_time, upload_bytes, download_bytes
)
SELECT
	id, node_id, member_id, inbound_id, member_name, inbound_tag,
	granularity, bucket_time, upload_bytes, download_bytes
FROM traffic_stats;

DROP TABLE traffic_stats;
ALTER TABLE traffic_stats_m3_new RENAME TO traffic_stats;

CREATE INDEX IF NOT EXISTS idx_traffic_stats_bucket ON traffic_stats(granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_member ON traffic_stats(member_name, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_inbound ON traffic_stats(inbound_tag, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_member_id ON traffic_stats(member_id, granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_inbound_id ON traffic_stats(inbound_id, granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_node_id ON traffic_stats(node_id, granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_certificates_node_id ON certificates(node_id);
CREATE INDEX IF NOT EXISTS idx_outbounds_extra_node_id ON outbounds_extra(node_id);
CREATE INDEX IF NOT EXISTS idx_route_rules_node_id ON route_rules(node_id);
`
		if _, err := tx.ExecContext(ctx, rebuildSQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec migration 3 rebuild: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (3, 'milestone3_multi_node')`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration 3: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration 3: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
			return fmt.Errorf("re-enable foreign_keys: %w", err)
		}
	}

	// 4. Seed local node id=1 idempotently, copying existing settings values on first migration.
	return s.ensureLocalNode(ctx)
}

func (s *Store) ensureColumn(ctx context.Context, table, column, def string) error {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if strings.EqualFold(name, column) {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, def))
	if err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *Store) ensureLocalNode(ctx context.Context) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM nodes WHERE id = 1`).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	all, _ := s.GetAllSettings(ctx)
	name := strings.TrimSpace(all[model.SettingInstanceID])
	if name == "" {
		name = "local-master"
	}
	logLevel := strings.TrimSpace(all[model.SettingLogLevel])
	if logLevel == "" {
		logLevel = "info"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (
			id, name, is_local, token, public_ipv4, public_ipv6,
			override_patch, log_level, github_proxy, note
		) VALUES (1, ?, 1, '', ?, ?, ?, ?, ?, 'Local Master Node')
	`,
		name,
		strings.TrimSpace(all[model.SettingPublicIPv4]),
		strings.TrimSpace(all[model.SettingPublicIPv6]),
		all[model.SettingOverridePatch],
		logLevel,
		strings.TrimSpace(all[model.SettingGithubProxy]),
	)
	if err != nil {
		return fmt.Errorf("seed local node id=1: %w", err)
	}
	return nil
}

// --- Nodes CRUD ---

const nodeSelectColumns = `
	id, name, is_local, token, public_ipv4, public_ipv6,
	override_patch, log_level, github_proxy,
	os, arch, hostname, agent_version, core_version, data_dir,
	last_seen_at, note, created_at, updated_at
`

func scanNode(scanner interface{ Scan(dest ...any) error }) (*model.Node, error) {
	var n model.Node
	var isLocal int
	var lastSeen sql.NullTime
	err := scanner.Scan(
		&n.ID, &n.Name, &isLocal, &n.Token, &n.PublicIPv4, &n.PublicIPv6,
		&n.OverridePatch, &n.LogLevel, &n.GithubProxy,
		&n.OS, &n.Arch, &n.Hostname, &n.AgentVersion, &n.CoreVersion, &n.DataDir,
		&lastSeen, &n.Note, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	n.IsLocal = isLocal != 0
	if n.LogLevel == "" {
		n.LogLevel = "info"
	}
	if lastSeen.Valid {
		t := lastSeen.Time.UTC()
		n.LastSeenAt = &t
	}
	return &n, nil
}

func (s *Store) ListNodes(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeSelectColumns+` FROM nodes ORDER BY is_local DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.Node, 0)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *n)
	}
	return list, rows.Err()
}

func (s *Store) GetNode(ctx context.Context, id int64) (*model.Node, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeSelectColumns+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

func (s *Store) GetNodeByToken(ctx context.Context, token string) (*model.Node, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeSelectColumns+` FROM nodes WHERE token = ? AND is_local = 0`, token)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

func (s *Store) CreateNode(ctx context.Context, n *model.Node) error {
	if strings.TrimSpace(n.LogLevel) == "" {
		n.LogLevel = "info"
	}
	if !n.IsLocal && strings.TrimSpace(n.Token) == "" {
		tok, err := RandomHex(32)
		if err != nil {
			return err
		}
		n.Token = tok
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (
			name, is_local, token, public_ipv4, public_ipv6,
			override_patch, log_level, github_proxy, note
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		strings.TrimSpace(n.Name), boolToInt(n.IsLocal), n.Token,
		strings.TrimSpace(n.PublicIPv4), strings.TrimSpace(n.PublicIPv6),
		n.OverridePatch, n.LogLevel, strings.TrimSpace(n.GithubProxy), strings.TrimSpace(n.Note),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	n.ID = id
	if saved, err := s.GetNode(ctx, id); err == nil {
		*n = *saved
	}
	return nil
}

func (s *Store) UpdateNode(ctx context.Context, n *model.Node) error {
	if strings.TrimSpace(n.LogLevel) == "" {
		n.LogLevel = "info"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET
			name = ?, public_ipv4 = ?, public_ipv6 = ?,
			override_patch = ?, log_level = ?, github_proxy = ?, note = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		strings.TrimSpace(n.Name), strings.TrimSpace(n.PublicIPv4), strings.TrimSpace(n.PublicIPv6),
		n.OverridePatch, n.LogLevel, strings.TrimSpace(n.GithubProxy), strings.TrimSpace(n.Note),
		n.ID,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetNode(ctx, n.ID); err == nil {
		*n = *saved
	}
	return nil
}

// NodeRuntimeUpdate holds agent/local runtime fields reported on connect/heartbeat.
type NodeRuntimeUpdate struct {
	OS           string
	Arch         string
	Hostname     string
	AgentVersion string
	CoreVersion  string
	DataDir      string
	PublicIPv4   string
	PublicIPv6   string
	LastSeenAt   time.Time
}

// UpdateNodeRuntimeInfo persists runtime system info and auto-fills public IPs if currently empty.
func (s *Store) UpdateNodeRuntimeInfo(ctx context.Context, nodeID int64, info NodeRuntimeUpdate) error {
	ts := info.LastSeenAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET
			os = CASE WHEN ? != '' THEN ? ELSE os END,
			arch = CASE WHEN ? != '' THEN ? ELSE arch END,
			hostname = CASE WHEN ? != '' THEN ? ELSE hostname END,
			agent_version = CASE WHEN ? != '' THEN ? ELSE agent_version END,
			core_version = CASE WHEN ? != '' THEN ? ELSE core_version END,
			data_dir = CASE WHEN ? != '' THEN ? ELSE data_dir END,
			public_ipv4 = CASE WHEN public_ipv4 = '' AND ? != '' THEN ? ELSE public_ipv4 END,
			public_ipv6 = CASE WHEN public_ipv6 = '' AND ? != '' THEN ? ELSE public_ipv6 END,
			last_seen_at = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		info.OS, info.OS,
		info.Arch, info.Arch,
		info.Hostname, info.Hostname,
		info.AgentVersion, info.AgentVersion,
		info.CoreVersion, info.CoreVersion,
		info.DataDir, info.DataDir,
		info.PublicIPv4, info.PublicIPv4,
		info.PublicIPv6, info.PublicIPv6,
		ts.UTC(),
		nodeID,
	)
	return err
}

func (s *Store) ResetNodeToken(ctx context.Context, id int64) (string, error) {
	if id == 1 {
		return "", errors.New("cannot reset token on local master node")
	}
	newToken, err := RandomHex(32)
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET token = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND is_local = 0
	`, newToken, id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "", ErrNotFound
	}
	return newToken, nil
}

func (s *Store) CountNodeResources(ctx context.Context, nodeID int64) (inboundsCount int, certsCount int, err error) {
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM inbounds WHERE node_id = ?`, nodeID).Scan(&inboundsCount); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM certificates WHERE node_id = ?`, nodeID).Scan(&certsCount); err != nil {
		return 0, 0, err
	}
	return inboundsCount, certsCount, nil
}

func (s *Store) InboundCountsByNode(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, COUNT(1) FROM inbounds GROUP BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var nid int64
		var cnt int
		if err := rows.Scan(&nid, &cnt); err != nil {
			return nil, err
		}
		out[nid] = cnt
	}
	return out, rows.Err()
}

func (s *Store) DeleteNode(ctx context.Context, id int64, force bool) error {
	if id == 1 {
		return errors.New("cannot delete local master node")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if force {
		if _, err := tx.ExecContext(ctx, `DELETE FROM inbounds WHERE node_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM certificates WHERE node_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM outbounds_extra WHERE node_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM route_rules WHERE node_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_stats WHERE node_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id = ? AND is_local = 0`, id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}
	return tx.Commit()
}
