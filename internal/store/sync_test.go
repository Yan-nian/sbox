package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

func TestMilestone5SyncTargetMigrationAndCRUD(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-m4.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	ctx := context.Background()

	// Seed a legacy M1/M2 schema with an existing `sync_targets` row (legacy columns: target_type, auth_token_enc, target_ref, sync_mode, sub_format).
	if _, err := rawDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, m := range model.Migrations {
		if _, err := rawDB.ExecContext(ctx, m.SQL); err != nil {
			t.Fatalf("exec migration %d: %v", m.Version, err)
		}
		if _, err := rawDB.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.Version, m.Name); err != nil {
			t.Fatalf("record migration %d: %v", m.Version, err)
		}
	}
	if _, err := rawDB.ExecContext(ctx, `
		INSERT INTO sync_targets (name, target_type, enabled, endpoint_url, auth_token_enc, target_ref, sync_mode, sub_format)
		VALUES ('Legacy Gist', 'github_gist', 1, '', 'ghp_legacy_secret', 'gist-12345', 'merge_by_instance', 'uri')
	`); err != nil {
		t.Fatalf("seed legacy sync_target: %v", err)
	}
	_ = rawDB.Close()

	// Open with Milestone 5 Store -> must migrate cleanly with zero data loss.
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open migrated DB: %v", err)
	}
	defer st.Close()

	targets, err := st.ListSyncTargets(ctx)
	if err != nil {
		t.Fatalf("ListSyncTargets after migration: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 migrated sync target, got %d", len(targets))
	}
	if targets[0].Name != "Legacy Gist" || targets[0].Type != "github_gist" ||
		targets[0].MergeMode != "merge_by_instance" || targets[0].Format != "uri" ||
		targets[0].Config.Token != "ghp_legacy_secret" || targets[0].Config.GistID != "gist-12345" {
		t.Fatalf("unexpected migrated sync target: %+v", targets[0])
	}

	// Verify ToView masks secret token.
	view := targets[0].ToView()
	if !view.HasToken || view.Config.GistID != "gist-12345" {
		t.Fatalf("unexpected SyncTargetView: %+v", view)
	}

	// Create new M5 SyncTarget (must succeed without legacy `target_type NOT NULL` constraint errors).
	t2 := &model.SyncTarget{
		Name:      "Sub-Store Main",
		Enabled:   true,
		Type:      "sub_store",
		MemberID:  1,
		Format:    "base64",
		MergeMode: "replace",
		AutoPush:  true,
		Config: model.SyncTargetConfig{
			SubStoreURL:  "https://sub.example.com/secret",
			SubStoreName: "sbox-hk",
		},
	}
	if err := st.CreateSyncTarget(ctx, t2); err != nil {
		t.Fatalf("CreateSyncTarget: %v", err)
	}
	if t2.ID == 0 || !t2.AutoPush {
		t.Fatalf("unexpected created target: %+v", t2)
	}

	// Update target & push status.
	t2.Name = "Sub-Store Updated"
	if err := st.UpdateSyncTarget(ctx, t2); err != nil {
		t.Fatalf("UpdateSyncTarget: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpdateSyncTargetStatus(ctx, t2.ID, now, "success", "", "https://sub.example.com/secret/download/sbox-hk"); err != nil {
		t.Fatalf("UpdateSyncTargetStatus: %v", err)
	}
	got, err := st.GetSyncTarget(ctx, t2.ID)
	if err != nil {
		t.Fatalf("GetSyncTarget: %v", err)
	}
	if got.Name != "Sub-Store Updated" || got.LastStatus != "success" || got.LastRemoteURL != "https://sub.example.com/secret/download/sbox-hk" || got.LastPushedAt == nil {
		t.Fatalf("unexpected updated target: %+v", got)
	}

	// Delete target.
	if err := st.DeleteSyncTarget(ctx, t2.ID); err != nil {
		t.Fatalf("DeleteSyncTarget: %v", err)
	}
	if _, err := st.GetSyncTarget(ctx, t2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMilestone5SyncHistoryPruningAndRemoteBackupSettings(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Insert 215 history rows -> auto-pruning must keep strictly the latest 200 rows.
	for i := 1; i <= 215; i++ {
		if err := st.RecordSyncHistory(ctx, &model.SyncHistory{
			TargetID:   1,
			TargetName: "Gist",
			Success:    i%2 == 0,
			HTTPStatus: 200,
			NodeCount:  i,
			Message:    fmt.Sprintf("push #%d", i),
		}); err != nil {
			t.Fatalf("RecordSyncHistory #%d: %v", i, err)
		}
	}

	history, err := st.ListSyncHistory(ctx, 200)
	if err != nil {
		t.Fatalf("ListSyncHistory: %v", err)
	}
	if len(history) != 200 {
		t.Fatalf("expected pruned history len=200, got %d", len(history))
	}
	if history[0].NodeCount != 215 || history[199].NodeCount != 16 {
		t.Fatalf("expected newest=215 oldest=16, got newest=%d oldest=%d", history[0].NodeCount, history[199].NodeCount)
	}

	// Remote backup config defaults + round-trip.
	defCfg, lastAt, lastErr, err := st.GetRemoteBackupConfig(ctx)
	if err != nil {
		t.Fatalf("GetRemoteBackupConfig default: %v", err)
	}
	if defCfg.Type != "github_repo" || defCfg.Branch != "main" || defCfg.KeepCount != 5 || defCfg.IntervalHours != 24 || lastAt != "" || lastErr != "" {
		t.Fatalf("unexpected default remote backup config: %+v lastAt=%q lastErr=%q", defCfg, lastAt, lastErr)
	}

	defCfg.Enabled = true
	defCfg.Repo = "owner/backup-repo"
	defCfg.Token = "ghp_secret_tok"
	defCfg.Passphrase = "aes-master-pass"
	defCfg.KeepCount = 7
	defCfg.IntervalHours = 12
	if err := st.SaveRemoteBackupConfig(ctx, defCfg); err != nil {
		t.Fatalf("SaveRemoteBackupConfig: %v", err)
	}
	if err := st.RecordRemoteBackupStatus(ctx, "2026-11-27T12:00:00Z", ""); err != nil {
		t.Fatalf("RecordRemoteBackupStatus: %v", err)
	}
	loaded, loadedAt, loadedErr, err := st.GetRemoteBackupConfig(ctx)
	if err != nil {
		t.Fatalf("GetRemoteBackupConfig after save: %v", err)
	}
	if !loaded.Enabled || loaded.Repo != "owner/backup-repo" || loaded.Token != "ghp_secret_tok" || loaded.Passphrase != "aes-master-pass" || loadedAt != "2026-11-27T12:00:00Z" || loadedErr != "" {
		t.Fatalf("unexpected loaded remote backup config: %+v at=%q err=%q", loaded, loadedAt, loadedErr)
	}
}

func TestStoreWithClosedDBRestoreSafety(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "panel.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.SetSetting(ctx, "marker", "before"); err != nil {
		t.Fatalf("SetSetting before: %v", err)
	}
	if err := st.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}

	// Copy the DB file to a backup copy while closed, then mutate DB, then swap back via WithClosedDB.
	backupCopy := filepath.Join(dir, "panel.bak.db")
	if err := st.WithClosedDB(ctx, func(p string) error {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(backupCopy, raw, 0o600)
	}); err != nil {
		t.Fatalf("WithClosedDB snapshot copy: %v", err)
	}

	if err := st.SetSetting(ctx, "marker", "mutated"); err != nil {
		t.Fatalf("SetSetting mutated: %v", err)
	}

	// Simulate failed restore callback -> DB MUST still reopen and remain usable.
	errBoom := errors.New("wrong passphrase")
	if err := st.WithClosedDB(ctx, func(string) error { return errBoom }); !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
	val, _, err := st.GetSetting(ctx, "marker")
	if err != nil || val != "mutated" {
		t.Fatalf("DB should remain operational after failed restore: val=%q err=%v", val, err)
	}

	// Simulate real restore replacing panel.db on Windows (removing -wal/-shm too).
	if err := st.WithClosedDB(ctx, func(p string) error {
		_ = os.Remove(p + "-wal")
		_ = os.Remove(p + "-shm")
		raw, err := os.ReadFile(backupCopy)
		if err != nil {
			return err
		}
		return os.WriteFile(p, raw, 0o600)
	}); err != nil {
		t.Fatalf("WithClosedDB restore: %v", err)
	}

	valAfter, _, err := st.GetSetting(ctx, "marker")
	if err != nil || valAfter != "before" {
		t.Fatalf("expected marker='before' after restore, got %q err=%v", valAfter, err)
	}
}
