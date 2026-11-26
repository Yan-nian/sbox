package model

// Migration defines a single versioned SQLite schema migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations contains the ordered list of SQLite schema migrations covering all 9 core tables.
var Migrations = []Migration{
	{
		Version: 1,
		Name:    "initial_schema_9_tables",
		SQL: `
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS certificates (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	domain TEXT NOT NULL,
	cert_type TEXT NOT NULL,
	dns_provider TEXT NOT NULL DEFAULT '',
	dns_credentials_enc TEXT NOT NULL DEFAULT '',
	cert_path TEXT NOT NULL DEFAULT '',
	key_path TEXT NOT NULL DEFAULT '',
	expires_at DATETIME,
	last_renew_status TEXT NOT NULL DEFAULT '',
	last_renew_error TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS members (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	enabled INTEGER NOT NULL DEFAULT 1,
	uuid TEXT NOT NULL UNIQUE,
	password TEXT NOT NULL,
	ss2022_key TEXT NOT NULL DEFAULT '',
	sub_token TEXT NOT NULL UNIQUE,
	upload_bytes INTEGER NOT NULL DEFAULT 0,
	download_bytes INTEGER NOT NULL DEFAULT 0,
	remark TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS inbounds (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	tag TEXT NOT NULL UNIQUE,
	protocol TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	listen TEXT NOT NULL DEFAULT '::',
	listen_port INTEGER NOT NULL,
	config_json TEXT NOT NULL DEFAULT '{}',
	certificate_id INTEGER REFERENCES certificates(id) ON DELETE SET NULL,
	member_bind_mode TEXT NOT NULL DEFAULT 'all',
	member_ids_json TEXT NOT NULL DEFAULT '[]',
	external_entries_json TEXT NOT NULL DEFAULT '[]',
	upload_bytes INTEGER NOT NULL DEFAULT 0,
	download_bytes INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS upstream_subs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	update_cron TEXT NOT NULL DEFAULT '0 */6 * * *',
	include_regex TEXT NOT NULL DEFAULT '',
	exclude_regex TEXT NOT NULL DEFAULT '',
	selector_tag TEXT NOT NULL DEFAULT '',
	urltest_tag TEXT NOT NULL DEFAULT '',
	cached_nodes_json TEXT NOT NULL DEFAULT '[]',
	last_fetch_status TEXT NOT NULL DEFAULT '',
	last_fetch_error TEXT NOT NULL DEFAULT '',
	last_fetched_at DATETIME,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS outbounds_extra (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	tag TEXT NOT NULL UNIQUE,
	kind TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	config_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS route_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL DEFAULT '',
	priority INTEGER NOT NULL DEFAULT 100,
	preset_type TEXT NOT NULL DEFAULT 'custom',
	match_json TEXT NOT NULL DEFAULT '{}',
	action TEXT NOT NULL DEFAULT 'route',
	outbound_tag TEXT NOT NULL DEFAULT 'direct',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sync_targets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	target_type TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	endpoint_url TEXT NOT NULL DEFAULT '',
	auth_token_enc TEXT NOT NULL DEFAULT '',
	target_ref TEXT NOT NULL DEFAULT '',
	sync_mode TEXT NOT NULL DEFAULT 'independent',
	sub_format TEXT NOT NULL DEFAULT 'base64',
	last_sync_status TEXT NOT NULL DEFAULT '',
	last_sync_http_code INTEGER NOT NULL DEFAULT 0,
	last_sync_error TEXT NOT NULL DEFAULT '',
	last_synced_at DATETIME,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS traffic_stats (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	member_id INTEGER REFERENCES members(id) ON DELETE CASCADE,
	inbound_id INTEGER REFERENCES inbounds(id) ON DELETE CASCADE,
	member_name TEXT NOT NULL DEFAULT '',
	inbound_tag TEXT NOT NULL DEFAULT '',
	granularity TEXT NOT NULL DEFAULT 'hour',
	bucket_time DATETIME NOT NULL,
	upload_bytes INTEGER NOT NULL DEFAULT 0,
	download_bytes INTEGER NOT NULL DEFAULT 0,
	UNIQUE(member_name, inbound_tag, granularity, bucket_time)
);

CREATE INDEX IF NOT EXISTS idx_inbounds_enabled ON inbounds(enabled);
CREATE INDEX IF NOT EXISTS idx_members_sub_token ON members(sub_token);
CREATE INDEX IF NOT EXISTS idx_route_rules_priority ON route_rules(priority, id);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_bucket ON traffic_stats(granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_member ON traffic_stats(member_name, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_inbound ON traffic_stats(inbound_tag, bucket_time);
`,
	},
	{
		Version: 2,
		Name:    "milestone2_certs_members_inbounds",
		SQL: `
ALTER TABLE certificates ADD COLUMN domains_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE certificates ADD COLUMN acme_email TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN acme_http_port TEXT NOT NULL DEFAULT '80';
ALTER TABLE certificates ADD COLUMN acme_staging INTEGER NOT NULL DEFAULT 0;
ALTER TABLE certificates ADD COLUMN issuer TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN not_before DATETIME;
ALTER TABLE certificates ADD COLUMN not_after DATETIME;
ALTER TABLE certificates ADD COLUMN auto_renew INTEGER NOT NULL DEFAULT 1;

ALTER TABLE members ADD COLUMN ss2022_key16 TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN ss2022_key32 TEXT NOT NULL DEFAULT '';

ALTER TABLE inbounds ADD COLUMN external_host TEXT NOT NULL DEFAULT '';
ALTER TABLE inbounds ADD COLUMN external_port INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_traffic_stats_member_id ON traffic_stats(member_id, granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_stats_inbound_id ON traffic_stats(inbound_id, granularity, bucket_time);
CREATE INDEX IF NOT EXISTS idx_inbounds_certificate_id ON inbounds(certificate_id);
`,
	},
}
