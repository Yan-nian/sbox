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

const maxSyncHistoryRows = 200

// ensureMilestone5Schema applies idempotent, zero-data-loss Milestone 5 upgrades for
// `sync_targets` and `sync_history`.
func (s *Store) ensureMilestone5Schema(ctx context.Context) error {
	// 1. Ensure `sync_history` table exists.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS sync_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id INTEGER NOT NULL DEFAULT 0,
			target_name TEXT NOT NULL DEFAULT '',
			success INTEGER NOT NULL DEFAULT 0,
			http_status INTEGER NOT NULL DEFAULT 0,
			node_count INTEGER NOT NULL DEFAULT 0,
			message TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_sync_history_id_desc ON sync_history(id DESC);
		CREATE INDEX IF NOT EXISTS idx_sync_history_target_id ON sync_history(target_id, id DESC);
	`); err != nil {
		return fmt.Errorf("create sync_history table: %w", err)
	}

	// 2. Idempotent PRAGMA table_info column upgrades on `sync_targets`.
	colSpecs := []struct {
		col string
		def string
	}{
		{"type", "TEXT NOT NULL DEFAULT 'github_gist'"},
		{"member_id", "INTEGER NOT NULL DEFAULT 0"},
		{"format", "TEXT NOT NULL DEFAULT 'base64'"},
		{"merge_mode", "TEXT NOT NULL DEFAULT 'replace'"},
		{"config_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"auto_push", "INTEGER NOT NULL DEFAULT 0"},
		{"last_pushed_at", "DATETIME"},
		{"last_status", "TEXT NOT NULL DEFAULT ''"},
		{"last_error", "TEXT NOT NULL DEFAULT ''"},
		{"last_remote_url", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, spec := range colSpecs {
		if err := s.ensureColumn(ctx, "sync_targets", spec.col, spec.def); err != nil {
			return err
		}
	}

	// 3. Version 5 migration: cleanly normalize legacy M1 `sync_targets` schema (removing
	// `target_type NOT NULL` constraint while preserving 100% of existing rows).
	var v5Applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = 5`).Scan(&v5Applied); err != nil {
		return fmt.Errorf("check migration 5: %w", err)
	}
	if v5Applied == 0 {
		hasLegacyCols := s.hasColumn(ctx, "sync_targets", "target_type")

		type legacyRow struct {
			id            int64
			name          string
			enabled       int
			targetType    string
			memberID      int64
			format        string
			mergeMode     string
			configJSON    string
			autoPush      int
			lastPushedAt  sql.NullTime
			lastStatus    string
			lastError     string
			lastRemoteURL string
			endpointURL   string
			authTokenEnc  string
			targetRef     string
			syncMode      string
			subFormat     string
			lastSyncStat  string
			lastSyncErr   string
			lastSyncedAt  sql.NullTime
			createdAt     time.Time
			updatedAt     time.Time
		}

		var migrated []legacyRow
		if hasLegacyCols {
			rows, err := s.db.QueryContext(ctx, `
				SELECT
					id, name, enabled, target_type, member_id, format, merge_mode,
					config_json, auto_push, last_pushed_at, last_status, last_error, last_remote_url,
					endpoint_url, auth_token_enc, target_ref, sync_mode, sub_format,
					last_sync_status, last_sync_error, last_synced_at, created_at, updated_at
				FROM sync_targets
			`)
			if err != nil {
				return fmt.Errorf("read legacy sync_targets: %w", err)
			}
			for rows.Next() {
				var r legacyRow
				if err := rows.Scan(
					&r.id, &r.name, &r.enabled, &r.targetType, &r.memberID, &r.format, &r.mergeMode,
					&r.configJSON, &r.autoPush, &r.lastPushedAt, &r.lastStatus, &r.lastError, &r.lastRemoteURL,
					&r.endpointURL, &r.authTokenEnc, &r.targetRef, &r.syncMode, &r.subFormat,
					&r.lastSyncStat, &r.lastSyncErr, &r.lastSyncedAt, &r.createdAt, &r.updatedAt,
				); err != nil {
					_ = rows.Close()
					return fmt.Errorf("scan legacy sync_targets: %w", err)
				}
				migrated = append(migrated, r)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			_ = rows.Close()
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration 5: %w", err)
		}

		if hasLegacyCols {
			rebuildSQL := `
CREATE TABLE sync_targets_m5_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	type TEXT NOT NULL DEFAULT 'github_gist',
	member_id INTEGER NOT NULL DEFAULT 0,
	format TEXT NOT NULL DEFAULT 'base64',
	merge_mode TEXT NOT NULL DEFAULT 'replace',
	config_json TEXT NOT NULL DEFAULT '{}',
	auto_push INTEGER NOT NULL DEFAULT 0,
	last_pushed_at DATETIME,
	last_status TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	last_remote_url TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
			if _, err := tx.ExecContext(ctx, rebuildSQL); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("create sync_targets_m5_new: %w", err)
			}

			for _, r := range migrated {
				tType := strings.TrimSpace(r.targetType)
				if tType == "" {
					tType = "github_gist"
				}
				format := strings.TrimSpace(r.subFormat)
				if format == "" {
					format = strings.TrimSpace(r.format)
				}
				if format == "" {
					format = "base64"
				}
				mergeMode := strings.TrimSpace(r.syncMode)
				if mergeMode == "" || mergeMode == "independent" {
					mergeMode = "replace"
				}
				cfgJSON := strings.TrimSpace(r.configJSON)
				if cfgJSON == "" || cfgJSON == "{}" {
					cfg := model.SyncTargetConfig{
						Token: r.authTokenEnc,
					}
					switch tType {
					case "github_gist":
						cfg.GistID = r.targetRef
					case "github_repo":
						cfg.Repo = r.targetRef
					case "sub_store":
						cfg.SubStoreURL = r.endpointURL
						cfg.SubStoreName = r.targetRef
					}
					b, _ := json.Marshal(cfg)
					cfgJSON = string(b)
				}
				lastPushed := r.lastPushedAt
				if !lastPushed.Valid && r.lastSyncedAt.Valid {
					lastPushed = r.lastSyncedAt
				}
				lastStatus := r.lastStatus
				if lastStatus == "" {
					lastStatus = r.lastSyncStat
				}
				lastErr := r.lastError
				if lastErr == "" {
					lastErr = r.lastSyncErr
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO sync_targets_m5_new (
						id, name, enabled, type, member_id, format, merge_mode, config_json, auto_push,
						last_pushed_at, last_status, last_error, last_remote_url, created_at, updated_at
					) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`,
					r.id, r.name, r.enabled, tType, r.memberID, format, mergeMode, cfgJSON, r.autoPush,
					lastPushed, lastStatus, lastErr, r.lastRemoteURL, r.createdAt, r.updatedAt,
				); err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("copy legacy sync_target %d: %w", r.id, err)
				}
			}

			if _, err := tx.ExecContext(ctx, `
				DROP TABLE sync_targets;
				ALTER TABLE sync_targets_m5_new RENAME TO sync_targets;
			`); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("swap sync_targets_m5_new: %w", err)
			}
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (5, 'milestone5_sync_backup')`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration 5: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration 5: %w", err)
		}
	}

	return nil
}

// --- Sync Targets CRUD ---

const syncTargetSelectColumns = `
	id, name, enabled, type, member_id, format, merge_mode,
	config_json, auto_push, last_pushed_at, last_status, last_error, last_remote_url,
	created_at, updated_at
`

func scanSyncTarget(scanner interface{ Scan(dest ...any) error }) (*model.SyncTarget, error) {
	var t model.SyncTarget
	var enabled, autoPush int
	var lastPushed sql.NullTime
	err := scanner.Scan(
		&t.ID, &t.Name, &enabled, &t.Type, &t.MemberID, &t.Format, &t.MergeMode,
		&t.ConfigJSON, &autoPush, &lastPushed, &t.LastStatus, &t.LastError, &t.LastRemoteURL,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.Enabled = enabled != 0
	t.AutoPush = autoPush != 0
	if lastPushed.Valid {
		ts := lastPushed.Time.UTC()
		t.LastPushedAt = &ts
	}
	if strings.TrimSpace(t.ConfigJSON) == "" {
		t.ConfigJSON = "{}"
	}
	_ = json.Unmarshal([]byte(t.ConfigJSON), &t.Config)
	if t.Format == "" {
		t.Format = "base64"
	}
	if t.MergeMode == "" {
		t.MergeMode = "replace"
	}
	return &t, nil
}

func normalizeSyncTarget(t *model.SyncTarget) string {
	t.Name = strings.TrimSpace(t.Name)
	t.Type = strings.ToLower(strings.TrimSpace(t.Type))
	t.Format = strings.ToLower(strings.TrimSpace(t.Format))
	if t.Format == "" {
		t.Format = "base64"
	}
	t.MergeMode = strings.ToLower(strings.TrimSpace(t.MergeMode))
	if t.MergeMode == "" {
		t.MergeMode = "replace"
	}
	b, _ := json.Marshal(t.Config)
	t.ConfigJSON = string(b)
	return t.ConfigJSON
}

func (s *Store) CreateSyncTarget(ctx context.Context, t *model.SyncTarget) error {
	cfgJSON := normalizeSyncTarget(t)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_targets (
			name, enabled, type, member_id, format, merge_mode, config_json, auto_push,
			last_pushed_at, last_status, last_error, last_remote_url
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		t.Name, boolToInt(t.Enabled), t.Type, t.MemberID, t.Format, t.MergeMode, cfgJSON, boolToInt(t.AutoPush),
		t.LastPushedAt, t.LastStatus, t.LastError, t.LastRemoteURL,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.ID = id
	if saved, err := s.GetSyncTarget(ctx, id); err == nil {
		*t = *saved
	}
	return nil
}

func (s *Store) GetSyncTarget(ctx context.Context, id int64) (*model.SyncTarget, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+syncTargetSelectColumns+` FROM sync_targets WHERE id = ?`, id)
	t, err := scanSyncTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Store) ListSyncTargets(ctx context.Context) ([]model.SyncTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+syncTargetSelectColumns+` FROM sync_targets ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.SyncTarget, 0)
	for rows.Next() {
		t, err := scanSyncTarget(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *t)
	}
	return list, rows.Err()
}

func (s *Store) UpdateSyncTarget(ctx context.Context, t *model.SyncTarget) error {
	cfgJSON := normalizeSyncTarget(t)
	res, err := s.db.ExecContext(ctx, `
		UPDATE sync_targets SET
			name = ?, enabled = ?, type = ?, member_id = ?, format = ?, merge_mode = ?,
			config_json = ?, auto_push = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		t.Name, boolToInt(t.Enabled), t.Type, t.MemberID, t.Format, t.MergeMode,
		cfgJSON, boolToInt(t.AutoPush), t.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if saved, err := s.GetSyncTarget(ctx, t.ID); err == nil {
		*t = *saved
	}
	return nil
}

// UpdateSyncTargetStatus updates push execution outcome fields on a sync target.
func (s *Store) UpdateSyncTargetStatus(ctx context.Context, id int64, pushedAt time.Time, status, errMsg, remoteURL string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sync_targets SET
			last_pushed_at = ?,
			last_status = ?,
			last_error = ?,
			last_remote_url = CASE WHEN ? != '' THEN ? ELSE last_remote_url END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, pushedAt.UTC(), status, errMsg, remoteURL, remoteURL, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSyncTarget(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sync_targets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Sync History CRUD (auto-pruned to latest 200 rows) ---

func (s *Store) RecordSyncHistory(ctx context.Context, h *model.SyncHistory) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_history (target_id, target_name, success, http_status, node_count, message)
		VALUES (?, ?, ?, ?, ?, ?)
	`, h.TargetID, h.TargetName, boolToInt(h.Success), h.HTTPStatus, h.NodeCount, h.Message)
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil {
		h.ID = id
	}
	// Automatically prune oldest rows beyond latest 200 entries.
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM sync_history
		WHERE id NOT IN (SELECT id FROM sync_history ORDER BY id DESC LIMIT ?)
	`, maxSyncHistoryRows)
	return nil
}

func (s *Store) ListSyncHistory(ctx context.Context, limit int) ([]model.SyncHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxSyncHistoryRows {
		limit = maxSyncHistoryRows
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, target_name, success, http_status, node_count, message, created_at
		FROM sync_history
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]model.SyncHistory, 0)
	for rows.Next() {
		var h model.SyncHistory
		var success int
		if err := rows.Scan(&h.ID, &h.TargetID, &h.TargetName, &success, &h.HTTPStatus, &h.NodeCount, &h.Message, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Success = success != 0
		list = append(list, h)
	}
	return list, rows.Err()
}

// --- Remote Backup Settings Helpers ---

func defaultRemoteBackupConfig() model.RemoteBackupConfig {
	return model.RemoteBackupConfig{
		Enabled:       false,
		Type:          "github_repo",
		Branch:        "main",
		PathPrefix:    "backups/sbox-panel",
		KeepCount:     5,
		IntervalHours: 24,
	}
}

func (s *Store) GetRemoteBackupConfig(ctx context.Context) (model.RemoteBackupConfig, string, string, error) {
	cfg := defaultRemoteBackupConfig()
	raw, ok, err := s.GetSetting(ctx, model.SettingBackupRemoteJSON)
	if err != nil {
		return cfg, "", "", err
	}
	if ok && strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.Type != "github_repo" && cfg.Type != "github_gist" {
		cfg.Type = "github_repo"
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		cfg.Branch = "main"
	}
	if strings.TrimSpace(cfg.PathPrefix) == "" {
		cfg.PathPrefix = "backups/sbox-panel"
	}
	if cfg.KeepCount <= 0 {
		cfg.KeepCount = 5
	}
	if cfg.IntervalHours < 0 {
		cfg.IntervalHours = 24
	}
	lastAt, _ := s.GetSettingDefault(ctx, model.SettingBackupLastAt, "")
	lastErr, _ := s.GetSettingDefault(ctx, model.SettingBackupLastError, "")
	return cfg, lastAt, lastErr, nil
}

func (s *Store) SaveRemoteBackupConfig(ctx context.Context, cfg model.RemoteBackupConfig) error {
	if cfg.Type != "github_repo" && cfg.Type != "github_gist" {
		cfg.Type = "github_repo"
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		cfg.Branch = "main"
	}
	if strings.TrimSpace(cfg.PathPrefix) == "" {
		cfg.PathPrefix = "backups/sbox-panel"
	}
	if cfg.KeepCount <= 0 {
		cfg.KeepCount = 5
	}
	if cfg.IntervalHours < 0 {
		cfg.IntervalHours = 24
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, model.SettingBackupRemoteJSON, string(b))
}

func (s *Store) RecordRemoteBackupStatus(ctx context.Context, lastAt, lastErr string) error {
	updates := map[string]string{
		model.SettingBackupLastError: lastErr,
	}
	if lastAt != "" {
		updates[model.SettingBackupLastAt] = lastAt
	}
	return s.SetSettings(ctx, updates)
}
