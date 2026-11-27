package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

// ensureMilestone4Schema applies idempotent, zero-data-loss Milestone 4 upgrades to `nodes`,
// `upstream_subs`, `outbounds_extra`, and `route_rules`.
func (s *Store) ensureMilestone4Schema(ctx context.Context) error {
	colSpecs := []struct {
		table string
		col   string
		def   string
	}{
		{"nodes", "default_outbound", "TEXT NOT NULL DEFAULT 'direct'"},

		{"upstream_subs", "tag", "TEXT NOT NULL DEFAULT ''"},
		{"upstream_subs", "interval_minutes", "INTEGER NOT NULL DEFAULT 360"},
		{"upstream_subs", "enable_urltest", "INTEGER NOT NULL DEFAULT 1"},
		{"upstream_subs", "test_url", "TEXT NOT NULL DEFAULT ''"},
		{"upstream_subs", "proxy_url", "TEXT NOT NULL DEFAULT ''"},
		{"upstream_subs", "cached_format", "TEXT NOT NULL DEFAULT ''"},
		{"upstream_subs", "node_count", "INTEGER NOT NULL DEFAULT 0"},

		{"route_rules", "preset_key", "TEXT NOT NULL DEFAULT ''"},
		{"route_rules", "rule_sets_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "domains_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "domain_suffix_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "domain_keyword_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "ip_cidr_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "protocols_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "ports_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "auth_users_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "inbounds_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"route_rules", "ip_is_private", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, spec := range colSpecs {
		if err := s.ensureColumn(ctx, spec.table, spec.col, spec.def); err != nil {
			return err
		}
	}

	// Backfill empty upstream_subs tags before enforcing unique index.
	if _, err := s.db.ExecContext(ctx, `UPDATE upstream_subs SET tag = 'sub-' || id WHERE tag = ''`); err != nil {
		return fmt.Errorf("backfill upstream_subs tags: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_subs_tag ON upstream_subs(tag) WHERE tag != ''`); err != nil {
		return fmt.Errorf("create idx_upstream_subs_tag: %w", err)
	}

	// Version 4 migration: rebuild `outbounds_extra` with per-node UNIQUE(node_id, tag) and `type`/`settings_json`.
	var v4Applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = 4`).Scan(&v4Applied); err != nil {
		return fmt.Errorf("check migration 4: %w", err)
	}
	if v4Applied == 0 {
		hasOldCols := s.hasColumn(ctx, "outbounds_extra", "kind") && !s.hasColumn(ctx, "outbounds_extra", "type")
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration 4: %w", err)
		}

		if hasOldCols {
			rebuildOutbounds := `
CREATE TABLE outbounds_extra_m4_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id INTEGER NOT NULL DEFAULT 1,
	name TEXT NOT NULL,
	tag TEXT NOT NULL,
	type TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	settings_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(node_id, tag)
);

INSERT INTO outbounds_extra_m4_new (id, node_id, name, tag, type, enabled, settings_json, created_at, updated_at)
SELECT
	id,
	node_id,
	name,
	tag,
	CASE
		WHEN kind = 'warp_endpoint' THEN 'warp'
		WHEN kind = 'manual' THEN 'raw'
		WHEN kind IN ('warp', 'wireguard', 'socks', 'http', 'raw') THEN kind
		ELSE 'raw'
	END,
	enabled,
	config_json,
	created_at,
	updated_at
FROM outbounds_extra;

DROP TABLE outbounds_extra;
ALTER TABLE outbounds_extra_m4_new RENAME TO outbounds_extra;
CREATE INDEX IF NOT EXISTS idx_outbounds_extra_node_id ON outbounds_extra(node_id);
`
			if _, err := tx.ExecContext(ctx, rebuildOutbounds); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("rebuild outbounds_extra in migration 4: %w", err)
			}
		}

		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_route_rules_node_priority ON route_rules(node_id, priority, id)`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("create route_rules index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (4, 'milestone4_routing_outbounds')`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration 4: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration 4: %w", err)
		}
	}

	return nil
}

func (s *Store) hasColumn(ctx context.Context, table, column string) bool {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
			if strings.EqualFold(name, column) {
				return true
			}
		}
	}
	return false
}

// SetNodeDefaultOutbound updates nodes.default_outbound for a given nodeID.
func (s *Store) SetNodeDefaultOutbound(ctx context.Context, nodeID int64, tag string) error {
	if nodeID <= 0 {
		nodeID = 1
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		tag = "direct"
	}
	res, err := s.db.ExecContext(ctx, `UPDATE nodes SET default_outbound = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, tag, nodeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Upstream Subscriptions CRUD ---

const upstreamSelectColumns = `
	id, node_id, name, tag, url, enabled,
	interval_minutes, include_regex, exclude_regex,
	enable_urltest, test_url, proxy_url,
	cached_format, cached_nodes_json, node_count,
	last_fetched_at, last_fetch_error,
	created_at, updated_at
`

func scanUpstreamSub(scanner interface{ Scan(dest ...any) error }) (*model.UpstreamSub, error) {
	var u model.UpstreamSub
	var enabled, enableURLTest int
	var fetched sql.NullTime
	err := scanner.Scan(
		&u.ID, &u.NodeID, &u.Name, &u.Tag, &u.URL, &enabled,
		&u.IntervalMinutes, &u.IncludeRegex, &u.ExcludeRegex,
		&enableURLTest, &u.TestURL, &u.ProxyURL,
		&u.CachedFormat, &u.CachedNodesJSON, &u.NodeCount,
		&fetched, &u.LastError,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled != 0
	u.EnableURLTest = enableURLTest != 0
	if fetched.Valid {
		t := fetched.Time.UTC()
		u.LastFetchedAt = &t
	}
	if u.CachedNodesJSON == "" {
		u.CachedNodesJSON = "[]"
	}
	return &u, nil
}

func (s *Store) CreateUpstreamSub(ctx context.Context, u *model.UpstreamSub) error {
	if u.NodeID < 0 {
		u.NodeID = 0
	}
	if u.IntervalMinutes < 0 {
		u.IntervalMinutes = 360
	}
	if u.CachedNodesJSON == "" {
		u.CachedNodesJSON = "[]"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO upstream_subs (
			node_id, name, tag, url, enabled,
			interval_minutes, include_regex, exclude_regex,
			enable_urltest, test_url, proxy_url,
			cached_format, cached_nodes_json, node_count,
			last_fetched_at, last_fetch_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		u.NodeID, strings.TrimSpace(u.Name), strings.TrimSpace(u.Tag), strings.TrimSpace(u.URL), boolToInt(u.Enabled),
		u.IntervalMinutes, u.IncludeRegex, u.ExcludeRegex,
		boolToInt(u.EnableURLTest), strings.TrimSpace(u.TestURL), strings.TrimSpace(u.ProxyURL),
		u.CachedFormat, u.CachedNodesJSON, u.NodeCount,
		u.LastFetchedAt, u.LastError,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	u.ID = id
	if strings.TrimSpace(u.Tag) == "" {
		autoTag := fmt.Sprintf("sub-%d", id)
		_, _ = s.db.ExecContext(ctx, `UPDATE upstream_subs SET tag = ? WHERE id = ?`, autoTag, id)
	}
	if saved, err := s.GetUpstreamSub(ctx, id); err == nil {
		*u = *saved
	}
	return nil
}

func (s *Store) GetUpstreamSub(ctx context.Context, id int64) (*model.UpstreamSub, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+upstreamSelectColumns+` FROM upstream_subs WHERE id = ?`, id)
	u, err := scanUpstreamSub(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ListUpstreamSubs lists upstream subscriptions.
// When nodeID > 0, returns global (node_id=0) + node-specific (node_id=nodeID) subscriptions.
// When nodeID <= 0, returns all subscriptions across all scopes.
func (s *Store) ListUpstreamSubs(ctx context.Context, nodeID int64) ([]model.UpstreamSub, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if nodeID > 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT `+upstreamSelectColumns+`
			FROM upstream_subs
			WHERE node_id = 0 OR node_id = ?
			ORDER BY node_id ASC, id ASC
		`, nodeID)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT `+upstreamSelectColumns+`
			FROM upstream_subs
			ORDER BY node_id ASC, id ASC
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.UpstreamSub, 0)
	for rows.Next() {
		u, err := scanUpstreamSub(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *u)
	}
	return list, rows.Err()
}

func (s *Store) UpdateUpstreamSub(ctx context.Context, u *model.UpstreamSub) error {
	if u.NodeID < 0 {
		u.NodeID = 0
	}
	if u.IntervalMinutes < 0 {
		u.IntervalMinutes = 360
	}
	if u.CachedNodesJSON == "" {
		u.CachedNodesJSON = "[]"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE upstream_subs SET
			node_id = ?, name = ?, tag = ?, url = ?, enabled = ?,
			interval_minutes = ?, include_regex = ?, exclude_regex = ?,
			enable_urltest = ?, test_url = ?, proxy_url = ?,
			cached_format = ?, cached_nodes_json = ?, node_count = ?,
			last_fetched_at = ?, last_fetch_error = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		u.NodeID, strings.TrimSpace(u.Name), strings.TrimSpace(u.Tag), strings.TrimSpace(u.URL), boolToInt(u.Enabled),
		u.IntervalMinutes, u.IncludeRegex, u.ExcludeRegex,
		boolToInt(u.EnableURLTest), strings.TrimSpace(u.TestURL), strings.TrimSpace(u.ProxyURL),
		u.CachedFormat, u.CachedNodesJSON, u.NodeCount,
		u.LastFetchedAt, u.LastError,
		u.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetUpstreamSub(ctx, u.ID); err == nil {
		*u = *saved
	}
	return nil
}

func (s *Store) DeleteUpstreamSub(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM upstream_subs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDueUpstreamSubs returns enabled subscriptions whose interval_minutes has elapsed since last_fetched_at.
func (s *Store) ListDueUpstreamSubs(ctx context.Context, now time.Time) ([]model.UpstreamSub, error) {
	all, err := s.ListUpstreamSubs(ctx, 0)
	if err != nil {
		return nil, err
	}
	due := make([]model.UpstreamSub, 0)
	for _, u := range all {
		if !u.Enabled || u.IntervalMinutes <= 0 {
			continue
		}
		if u.LastFetchedAt == nil || now.UTC().Sub(u.LastFetchedAt.UTC()) >= time.Duration(u.IntervalMinutes)*time.Minute {
			due = append(due, u)
		}
	}
	return due, nil
}

// --- Extra Outbounds & WARP CRUD ---

const outboundExtraSelectColumns = `
	id, node_id, name, tag, type, enabled, settings_json, created_at, updated_at
`

func scanOutboundExtra(scanner interface{ Scan(dest ...any) error }) (*model.OutboundExtra, error) {
	var o model.OutboundExtra
	var enabled int
	var settingsStr string
	err := scanner.Scan(
		&o.ID, &o.NodeID, &o.Name, &o.Tag, &o.Type, &enabled, &settingsStr, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if o.NodeID <= 0 {
		o.NodeID = 1
	}
	o.Enabled = enabled != 0
	settingsStr = strings.TrimSpace(settingsStr)
	if settingsStr == "" {
		settingsStr = "{}"
	}
	o.Settings = json.RawMessage(settingsStr)
	return &o, nil
}

func (s *Store) CreateOutboundExtra(ctx context.Context, o *model.OutboundExtra) error {
	if o.NodeID <= 0 {
		o.NodeID = 1
	}
	settingsStr := strings.TrimSpace(string(o.Settings))
	if settingsStr == "" {
		settingsStr = "{}"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO outbounds_extra (node_id, name, tag, type, enabled, settings_json)
		VALUES (?, ?, ?, ?, ?, ?)
	`, o.NodeID, strings.TrimSpace(o.Name), strings.TrimSpace(o.Tag), strings.ToLower(strings.TrimSpace(o.Type)), boolToInt(o.Enabled), settingsStr)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	o.ID = id
	if saved, err := s.GetOutboundExtra(ctx, id); err == nil {
		*o = *saved
	}
	return nil
}

func (s *Store) GetOutboundExtra(ctx context.Context, id int64) (*model.OutboundExtra, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+outboundExtraSelectColumns+` FROM outbounds_extra WHERE id = ?`, id)
	o, err := scanOutboundExtra(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *Store) ListOutboundsExtra(ctx context.Context, nodeID int64) ([]model.OutboundExtra, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if nodeID > 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT `+outboundExtraSelectColumns+` FROM outbounds_extra WHERE node_id = ? ORDER BY id ASC`, nodeID)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+outboundExtraSelectColumns+` FROM outbounds_extra ORDER BY node_id ASC, id ASC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.OutboundExtra, 0)
	for rows.Next() {
		o, err := scanOutboundExtra(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *o)
	}
	return list, rows.Err()
}

func (s *Store) UpdateOutboundExtra(ctx context.Context, o *model.OutboundExtra) error {
	if o.NodeID <= 0 {
		o.NodeID = 1
	}
	settingsStr := strings.TrimSpace(string(o.Settings))
	if settingsStr == "" {
		settingsStr = "{}"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE outbounds_extra SET
			node_id = ?, name = ?, tag = ?, type = ?, enabled = ?, settings_json = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, o.NodeID, strings.TrimSpace(o.Name), strings.TrimSpace(o.Tag), strings.ToLower(strings.TrimSpace(o.Type)), boolToInt(o.Enabled), settingsStr, o.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetOutboundExtra(ctx, o.ID); err == nil {
		*o = *saved
	}
	return nil
}

func (s *Store) ToggleOutboundExtra(ctx context.Context, id int64) (*model.OutboundExtra, error) {
	o, err := s.GetOutboundExtra(ctx, id)
	if err != nil {
		return nil, err
	}
	o.Enabled = !o.Enabled
	if err := s.UpdateOutboundExtra(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) DeleteOutboundExtra(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM outbounds_extra WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Route Rules CRUD ---

const routeRuleSelectColumns = `
	id, node_id, name, preset_key, priority, enabled, action, outbound_tag,
	rule_sets_json, domains_json, domain_suffix_json, domain_keyword_json,
	ip_cidr_json, protocols_json, ports_json, auth_users_json, inbounds_json,
	ip_is_private, created_at, updated_at
`

func marshalStringSlice(list []string) string {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func marshalIntSlice(list []int) string {
	if list == nil {
		list = []int{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func unmarshalStringSlice(raw string) []string {
	var out []string
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	if out == nil {
		return []string{}
	}
	return out
}

func unmarshalIntSlice(raw string) []int {
	var out []int
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	if out == nil {
		return []int{}
	}
	return out
}

func scanRouteRule(scanner interface{ Scan(dest ...any) error }) (*model.RouteRule, error) {
	var r model.RouteRule
	var enabled, ipPrivate int
	var ruleSetsJ, domainsJ, suffixJ, keywordJ, ipCIDRJ, protosJ, portsJ, usersJ, inboundsJ string
	err := scanner.Scan(
		&r.ID, &r.NodeID, &r.Name, &r.PresetKey, &r.Priority, &enabled, &r.Action, &r.OutboundTag,
		&ruleSetsJ, &domainsJ, &suffixJ, &keywordJ,
		&ipCIDRJ, &protosJ, &portsJ, &usersJ, &inboundsJ,
		&ipPrivate, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if r.NodeID <= 0 {
		r.NodeID = 1
	}
	r.Enabled = enabled != 0
	r.IPIsPrivate = ipPrivate != 0
	r.RuleSets = unmarshalStringSlice(ruleSetsJ)
	r.Domains = unmarshalStringSlice(domainsJ)
	r.DomainSuffix = unmarshalStringSlice(suffixJ)
	r.DomainKeyword = unmarshalStringSlice(keywordJ)
	r.IPCIDR = unmarshalStringSlice(ipCIDRJ)
	r.Protocols = unmarshalStringSlice(protosJ)
	r.Ports = unmarshalIntSlice(portsJ)
	r.AuthUsers = unmarshalStringSlice(usersJ)
	r.Inbounds = unmarshalStringSlice(inboundsJ)
	return &r, nil
}

func (s *Store) CreateRouteRule(ctx context.Context, r *model.RouteRule) error {
	if r.NodeID <= 0 {
		r.NodeID = 1
	}
	if strings.TrimSpace(r.Action) == "" {
		r.Action = "route"
	}
	if r.Priority <= 0 {
		var maxP int
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(priority), 0) FROM route_rules WHERE node_id = ?`, r.NodeID).Scan(&maxP)
		r.Priority = maxP + 10
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO route_rules (
			node_id, name, preset_key, priority, enabled, action, outbound_tag,
			rule_sets_json, domains_json, domain_suffix_json, domain_keyword_json,
			ip_cidr_json, protocols_json, ports_json, auth_users_json, inbounds_json,
			ip_is_private
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		r.NodeID, strings.TrimSpace(r.Name), strings.TrimSpace(r.PresetKey), r.Priority, boolToInt(r.Enabled),
		strings.TrimSpace(r.Action), strings.TrimSpace(r.OutboundTag),
		marshalStringSlice(r.RuleSets), marshalStringSlice(r.Domains), marshalStringSlice(r.DomainSuffix), marshalStringSlice(r.DomainKeyword),
		marshalStringSlice(r.IPCIDR), marshalStringSlice(r.Protocols), marshalIntSlice(r.Ports), marshalStringSlice(r.AuthUsers), marshalStringSlice(r.Inbounds),
		boolToInt(r.IPIsPrivate),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	if saved, err := s.GetRouteRule(ctx, id); err == nil {
		*r = *saved
	}
	return nil
}

func (s *Store) GetRouteRule(ctx context.Context, id int64) (*model.RouteRule, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+routeRuleSelectColumns+` FROM route_rules WHERE id = ?`, id)
	r, err := scanRouteRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) FindRouteRuleByPresetKey(ctx context.Context, nodeID int64, presetKey string) (*model.RouteRule, error) {
	if nodeID <= 0 {
		nodeID = 1
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+routeRuleSelectColumns+` FROM route_rules WHERE node_id = ? AND preset_key = ? LIMIT 1`, nodeID, presetKey)
	r, err := scanRouteRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) ListRouteRules(ctx context.Context, nodeID int64) ([]model.RouteRule, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if nodeID > 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT `+routeRuleSelectColumns+` FROM route_rules WHERE node_id = ? ORDER BY priority ASC, id ASC`, nodeID)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+routeRuleSelectColumns+` FROM route_rules ORDER BY node_id ASC, priority ASC, id ASC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.RouteRule, 0)
	for rows.Next() {
		r, err := scanRouteRule(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *r)
	}
	return list, rows.Err()
}

func (s *Store) UpdateRouteRule(ctx context.Context, r *model.RouteRule) error {
	if r.NodeID <= 0 {
		r.NodeID = 1
	}
	if strings.TrimSpace(r.Action) == "" {
		r.Action = "route"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE route_rules SET
			node_id = ?, name = ?, preset_key = ?, priority = ?, enabled = ?, action = ?, outbound_tag = ?,
			rule_sets_json = ?, domains_json = ?, domain_suffix_json = ?, domain_keyword_json = ?,
			ip_cidr_json = ?, protocols_json = ?, ports_json = ?, auth_users_json = ?, inbounds_json = ?,
			ip_is_private = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		r.NodeID, strings.TrimSpace(r.Name), strings.TrimSpace(r.PresetKey), r.Priority, boolToInt(r.Enabled),
		strings.TrimSpace(r.Action), strings.TrimSpace(r.OutboundTag),
		marshalStringSlice(r.RuleSets), marshalStringSlice(r.Domains), marshalStringSlice(r.DomainSuffix), marshalStringSlice(r.DomainKeyword),
		marshalStringSlice(r.IPCIDR), marshalStringSlice(r.Protocols), marshalIntSlice(r.Ports), marshalStringSlice(r.AuthUsers), marshalStringSlice(r.Inbounds),
		boolToInt(r.IPIsPrivate), r.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetRouteRule(ctx, r.ID); err == nil {
		*r = *saved
	}
	return nil
}

func (s *Store) DeleteRouteRule(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM route_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderRouteRules assigns monotonically increasing priorities (10, 20, 30, ...) according to ruleIDs order.
func (s *Store) ReorderRouteRules(ctx context.Context, nodeID int64, ruleIDs []int64) error {
	if nodeID <= 0 {
		nodeID = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE route_rules SET priority = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND node_id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for i, id := range ruleIDs {
		priority := (i + 1) * 10
		if _, err := stmt.ExecContext(ctx, priority, id, nodeID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
