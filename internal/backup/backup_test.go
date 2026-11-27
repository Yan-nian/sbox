package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func createTestWALDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE members (id INTEGER PRIMARY KEY, name TEXT NOT NULL, sub_token TEXT NOT NULL);`,
		`INSERT INTO members (id, name, sub_token) VALUES (1, 'alice', 'tok-alice'), (2, 'bob', 'tok-bob');`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
}

func queryMembers(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, sub_token FROM members`)
	if err != nil {
		t.Fatalf("query members: %v", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var name, tok string
		if err := rows.Scan(&name, &tok); err != nil {
			t.Fatalf("scan member: %v", err)
		}
		out[name] = tok
	}
	return out
}

func TestCreateAndRestoreArchivePlainAndEncrypted(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	dbPath := filepath.Join(tmp, "panel.db")
	certsDir := filepath.Join(tmp, "certs")
	createTestWALDB(t, dbPath)

	// Populate nested certificate files
	if err := os.MkdirAll(filepath.Join(certsDir, "self"), 0o700); err != nil {
		t.Fatalf("mkdir certs/self: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(certsDir, "acme"), 0o700); err != nil {
		t.Fatalf("mkdir certs/acme: %v", err)
	}
	certPEM := []byte("-----BEGIN CERTIFICATE-----\nTESTCERT\n-----END CERTIFICATE-----\n")
	keyPEM := []byte("-----BEGIN PRIVATE KEY-----\nTESTKEY\n-----END PRIVATE KEY-----\n")
	if err := os.WriteFile(filepath.Join(certsDir, "self", "cert.pem"), certPEM, 0o600); err != nil {
		t.Fatalf("write cert.pem: %v", err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "acme", "priv.key"), keyPEM, 0o600); err != nil {
		t.Fatalf("write priv.key: %v", err)
	}

	// 1. Plain archive export
	plainArchive, err := CreateArchive(ctx, ExportOptions{
		DBPath:   dbPath,
		CertsDir: certsDir,
	})
	if err != nil {
		t.Fatalf("CreateArchive(plain) failed: %v", err)
	}
	if IsEncrypted(plainArchive) {
		t.Fatalf("plain archive must not report IsEncrypted=true")
	}

	// 2. Encrypted archive export
	const pass = "Super$ecretPassphrase2026"
	encArchive, err := CreateArchive(ctx, ExportOptions{
		DBPath:     dbPath,
		CertsDir:   certsDir,
		Passphrase: pass,
	})
	if err != nil {
		t.Fatalf("CreateArchive(encrypted) failed: %v", err)
	}
	if !IsEncrypted(encArchive) {
		t.Fatalf("encrypted archive must report IsEncrypted=true")
	}
	if !bytes.HasPrefix(encArchive, []byte("SBOXENC1")) {
		t.Fatalf("encrypted archive missing SBOXENC1 magic prefix")
	}

	// 3. Wrong/missing passphrase errors
	restoreDir := filepath.Join(tmp, "restore-target")
	targetDB := filepath.Join(restoreDir, "panel.db")
	targetCerts := filepath.Join(restoreDir, "certs")

	if _, err := RestoreArchive(ctx, encArchive, "", targetDB, targetCerts); err == nil {
		t.Fatalf("expected error when restoring encrypted archive with empty passphrase")
	}
	if _, err := RestoreArchive(ctx, encArchive, "wrong-password", targetDB, targetCerts); err == nil {
		t.Fatalf("expected error when restoring encrypted archive with wrong passphrase")
	}

	// Tamper ciphertext -> must fail GCM authentication
	tampered := append([]byte(nil), encArchive...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := RestoreArchive(ctx, tampered, pass, targetDB, targetCerts); err == nil {
		t.Fatalf("expected error when restoring tampered encrypted archive")
	}

	// 4. Restore plain archive over an existing mutated DB + stale WAL/SHM + old certs
	if err := os.MkdirAll(targetCerts, 0o700); err != nil {
		t.Fatalf("mkdir target certs: %v", err)
	}
	createTestWALDB(t, targetDB)
	// Mutate targetDB: delete bob
	db, err := sql.Open("sqlite", targetDB)
	if err != nil {
		t.Fatalf("open targetDB: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM members WHERE name = 'bob'`); err != nil {
		t.Fatalf("delete bob: %v", err)
	}
	_ = db.Close()
	// Create stale -wal and -shm files and old cert
	_ = os.WriteFile(targetDB+"-wal", []byte("stale-wal"), 0o600)
	_ = os.WriteFile(targetDB+"-shm", []byte("stale-shm"), 0o600)
	_ = os.WriteFile(filepath.Join(targetCerts, "old.pem"), []byte("old"), 0o600)

	info, err := RestoreArchive(ctx, plainArchive, "", targetDB, targetCerts)
	if err != nil {
		t.Fatalf("RestoreArchive(plain) failed: %v", err)
	}
	if info.Encrypted || !info.HasDB || info.CertFiles != 2 || info.FileCount != 3 {
		t.Errorf("unexpected plain InspectResult: %+v", info)
	}
	// Verify stale -wal/-shm removed and .pre-restore siblings exist
	if _, err := os.Stat(targetDB + "-wal"); !os.IsNotExist(err) {
		t.Errorf("expected stale -wal file to be removed")
	}
	if _, err := os.Stat(targetDB + "-shm"); !os.IsNotExist(err) {
		t.Errorf("expected stale -shm file to be removed")
	}
	if _, err := os.Stat(targetDB + ".pre-restore"); err != nil {
		t.Errorf("expected %s.pre-restore backup to exist: %v", targetDB, err)
	}
	if _, err := os.Stat(filepath.Join(targetCerts+".pre-restore", "old.pem")); err != nil {
		t.Errorf("expected certs .pre-restore backup to exist: %v", err)
	}

	// Verify DB rows restored cleanly
	members := queryMembers(t, targetDB)
	if members["alice"] != "tok-alice" || members["bob"] != "tok-bob" {
		t.Errorf("restored DB members mismatch: %+v", members)
	}
	// Verify cert files restored cleanly
	gotCert, err := os.ReadFile(filepath.Join(targetCerts, "self", "cert.pem"))
	if err != nil || !bytes.Equal(gotCert, certPEM) {
		t.Errorf("restored cert.pem mismatch: %v", err)
	}
	gotKey, err := os.ReadFile(filepath.Join(targetCerts, "acme", "priv.key"))
	if err != nil || !bytes.Equal(gotKey, keyPEM) {
		t.Errorf("restored priv.key mismatch: %v", err)
	}

	// 5. Restore encrypted archive into fresh location
	encTargetDB := filepath.Join(tmp, "enc-restore", "panel.db")
	encTargetCerts := filepath.Join(tmp, "enc-restore", "certs")
	encInfo, err := RestoreArchive(ctx, encArchive, pass, encTargetDB, encTargetCerts)
	if err != nil {
		t.Fatalf("RestoreArchive(encrypted) failed: %v", err)
	}
	if !encInfo.Encrypted || !encInfo.HasDB || encInfo.CertFiles != 2 {
		t.Errorf("unexpected encrypted InspectResult: %+v", encInfo)
	}
	encMembers := queryMembers(t, encTargetDB)
	if encMembers["alice"] != "tok-alice" || encMembers["bob"] != "tok-bob" {
		t.Errorf("encrypted restore DB members mismatch: %+v", encMembers)
	}
}

func makeRawTarGz(t *testing.T, entries []struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o600,
			Size:     int64(len(e.body)),
			Typeflag: tf,
			Linkname: e.linkname,
			ModTime:  time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("write tar body: %v", err)
			}
		}
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func TestRestoreArchivePathTraversalAndInvalidDB(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	validSQLiteStub := append([]byte(sqliteHeaderMagic), make([]byte, 100)...)

	maliciousCases := []struct {
		name    string
		archive []byte
	}{
		{
			name: "dotdot traversal",
			archive: makeRawTarGz(t, []struct {
				name     string
				body     []byte
				typeflag byte
				linkname string
			}{
				{name: "panel.db", body: validSQLiteStub},
				{name: "../evil.txt", body: []byte("pwn")},
			}),
		},
		{
			name: "certs dotdot traversal",
			archive: makeRawTarGz(t, []struct {
				name     string
				body     []byte
				typeflag byte
				linkname string
			}{
				{name: "panel.db", body: validSQLiteStub},
				{name: "certs/../../escape.pem", body: []byte("pwn")},
			}),
		},
		{
			name: "absolute path",
			archive: makeRawTarGz(t, []struct {
				name     string
				body     []byte
				typeflag byte
				linkname string
			}{
				{name: "/etc/passwd", body: []byte("root:x:0:0")},
			}),
		},
		{
			name: "symlink entry",
			archive: makeRawTarGz(t, []struct {
				name     string
				body     []byte
				typeflag byte
				linkname string
			}{
				{name: "panel.db", body: validSQLiteStub},
				{name: "certs/link.pem", typeflag: tar.TypeSymlink, linkname: "/etc/shadow"},
			}),
		},
		{
			name: "invalid sqlite header",
			archive: makeRawTarGz(t, []struct {
				name     string
				body     []byte
				typeflag byte
				linkname string
			}{
				{name: "panel.db", body: []byte("NOT A SQLITE DATABASE FILE")},
			}),
		},
		{
			name: "missing panel.db",
			archive: makeRawTarGz(t, []struct {
				name     string
				body     []byte
				typeflag byte
				linkname string
			}{
				{name: "certs/a.pem", body: []byte("cert")},
			}),
		},
	}

	targetDB := filepath.Join(tmp, "existing.db")
	origContent := []byte("original-db-untouched")
	if err := os.WriteFile(targetDB, origContent, 0o600); err != nil {
		t.Fatalf("write existing db: %v", err)
	}

	for _, tc := range maliciousCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RestoreArchive(ctx, tc.archive, "", targetDB, filepath.Join(tmp, "certs"))
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}

	// Ensure existing targetDB was never modified by failed restores
	got, err := os.ReadFile(targetDB)
	if err != nil || !bytes.Equal(got, origContent) {
		t.Fatalf("existing targetDB was unexpectedly modified after rejected restores")
	}
}

func TestGitHubRepoBackupPushListPruneFetch(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	dbPath := filepath.Join(tmp, "panel.db")
	createTestWALDB(t, dbPath)
	const pass = "RepoBackupPass#1"
	encArchive, err := CreateArchive(ctx, ExportOptions{DBPath: dbPath, Passphrase: pass})
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Mock GitHub Repo Contents API
	var mu sync.Mutex
	files := make(map[string]struct {
		sha  string
		data []byte
	})
	seq := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		const prefix = "/repos/alice/backups/contents/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, prefix)

		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			// Exact file match
			if f, ok := files[rel]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name":     filepath.Base(rel),
					"path":     rel,
					"sha":      f.sha,
					"size":     len(f.data),
					"type":     "file",
					"encoding": "base64",
					"content":  base64.StdEncoding.EncodeToString(f.data),
				})
				return
			}
			// Directory listing
			dirPrefix := strings.TrimSuffix(rel, "/") + "/"
			var list []map[string]any
			for p, f := range files {
				if strings.HasPrefix(p, dirPrefix) {
					list = append(list, map[string]any{
						"name": strings.TrimPrefix(p, dirPrefix),
						"path": p,
						"sha":  f.sha,
						"size": len(f.data),
						"type": "file",
					})
				}
			}
			if len(list) == 0 {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(list)

		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
				Branch  string `json:"branch"`
				SHA     string `json:"sha"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			raw, err := base64.StdEncoding.DecodeString(body.Content)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			seq++
			sha := fmt.Sprintf("sha-%d", seq)
			files[rel] = struct {
				sha  string
				data []byte
			}{sha: sha, data: raw}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{
					"name": filepath.Base(rel),
					"path": rel,
					"sha":  sha,
					"size": len(raw),
				},
			})

		case http.MethodDelete:
			var body struct {
				SHA string `json:"sha"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f, ok := files[rel]
			if !ok || f.sha != body.SHA {
				http.Error(w, "sha mismatch", http.StatusConflict)
				return
			}
			delete(files, rel)
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"sha": "del"}})
		}
	}))
	defer srv.Close()

	cfg := RemoteGitHubConfig{
		Type:       "github_repo",
		Token:      "gh-test-token",
		Repo:       "alice/backups",
		Branch:     "main",
		PathPrefix: "backups/sbox-panel",
		KeepCount:  2,
		Passphrase: pass,
		APIBaseURL: srv.URL,
	}

	// Passphrase is REQUIRED for remote push
	badCfg := cfg
	badCfg.Passphrase = ""
	if _, err := PushGitHubBackup(ctx, encArchive, "test.sboxbak", badCfg); err == nil {
		t.Fatalf("expected error when pushing to GitHub without passphrase")
	}

	// Empty directory returns empty slice without error
	emptyList, err := ListGitHubBackups(ctx, cfg)
	if err != nil || len(emptyList) != 0 {
		t.Fatalf("expected empty list on 404, got len=%d err=%v", len(emptyList), err)
	}

	// Push 4 backups; KeepCount=2 should prune the oldest 2
	names := []string{
		"sbox-backup-20261127-010000.sboxbak",
		"sbox-backup-20261127-020000.sboxbak",
		"sbox-backup-20261127-030000.sboxbak",
		"sbox-backup-20261127-040000.sboxbak",
	}
	for _, n := range names {
		if _, err := PushGitHubBackup(ctx, encArchive, n, cfg); err != nil {
			t.Fatalf("PushGitHubBackup(%s) failed: %v", n, err)
		}
	}

	listed, err := ListGitHubBackups(ctx, cfg)
	if err != nil {
		t.Fatalf("ListGitHubBackups failed: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 backups retained after pruning, got %d: %+v", len(listed), listed)
	}
	if listed[0].Name != names[3] || listed[1].Name != names[2] {
		t.Errorf("expected newest 2 backups (%s, %s), got (%s, %s)", names[3], names[2], listed[0].Name, listed[1].Name)
	}

	// Fetch latest and restore to verify full integrity
	fetched, err := FetchGitHubBackup(ctx, listed[0].Path, cfg)
	if err != nil {
		t.Fatalf("FetchGitHubBackup failed: %v", err)
	}
	if !IsEncrypted(fetched) {
		t.Fatalf("fetched remote backup must be encrypted")
	}
	restoredDB := filepath.Join(tmp, "from-repo", "panel.db")
	res, err := RestoreArchive(ctx, fetched, pass, restoredDB, "")
	if err != nil {
		t.Fatalf("RestoreArchive from fetched repo backup failed: %v", err)
	}
	if !res.Encrypted || !res.HasDB {
		t.Errorf("unexpected inspect result: %+v", res)
	}
	members := queryMembers(t, restoredDB)
	if members["alice"] != "tok-alice" || members["bob"] != "tok-bob" {
		t.Errorf("restored repo DB members mismatch: %+v", members)
	}
}

func TestGitHubGistBackupPushListPruneFetch(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	dbPath := filepath.Join(tmp, "panel.db")
	createTestWALDB(t, dbPath)
	const pass = "GistBackupPass#2"
	// Pass a plain archive to verify PushGitHubBackup auto-encrypts it before upload!
	plainArchive, err := CreateArchive(ctx, ExportOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	var mu sync.Mutex
	gistFiles := map[string]string{
		"notes.md": "unrelated file must be preserved",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-gist-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/gists/gist-123" {
			http.NotFound(w, r)
			return
		}

		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			outFiles := make(map[string]any, len(gistFiles))
			for name, content := range gistFiles {
				outFiles[name] = map[string]any{
					"filename": name,
					"size":     len(content),
					"content":  content,
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "gist-123",
				"updated_at": "2026-11-27T08:00:00Z",
				"files":      outFiles,
			})
		case http.MethodPatch:
			var body struct {
				Files map[string]*struct {
					Content string `json:"content"`
				} `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for name, spec := range body.Files {
				if spec == nil {
					delete(gistFiles, name)
				} else {
					gistFiles[name] = spec.Content
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "gist-123"})
		}
	}))
	defer srv.Close()

	cfg := RemoteGitHubConfig{
		Type:       "github_gist",
		Token:      "gh-gist-token",
		GistID:     "gist-123",
		KeepCount:  2,
		Passphrase: pass,
		APIBaseURL: srv.URL,
	}

	for _, name := range []string{
		"sbox-backup-20261127-100000.sboxbak",
		"sbox-backup-20261127-110000.sboxbak",
		"sbox-backup-20261127-120000.sboxbak",
	} {
		entry, err := PushGitHubBackup(ctx, plainArchive, name, cfg)
		if err != nil {
			t.Fatalf("PushGitHubBackup(gist %s) failed: %v", name, err)
		}
		if !strings.HasSuffix(entry.Name, ".sboxbak.b64") {
			t.Errorf("expected gist backup name ending in .sboxbak.b64, got %q", entry.Name)
		}
	}

	// Ensure unrelated gist file was untouched
	mu.Lock()
	if gistFiles["notes.md"] != "unrelated file must be preserved" {
		t.Errorf("unrelated gist file was modified or deleted")
	}
	mu.Unlock()

	listed, err := ListGitHubBackups(ctx, cfg)
	if err != nil {
		t.Fatalf("ListGitHubBackups(gist) failed: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 gist backups after pruning, got %d: %+v", len(listed), listed)
	}
	if listed[0].Name != "sbox-backup-20261127-120000.sboxbak.b64" || listed[1].Name != "sbox-backup-20261127-110000.sboxbak.b64" {
		t.Errorf("unexpected gist backup list order: %+v", listed)
	}

	fetched, err := FetchGitHubBackup(ctx, listed[0].Path, cfg)
	if err != nil {
		t.Fatalf("FetchGitHubBackup(gist) failed: %v", err)
	}
	if !IsEncrypted(fetched) {
		t.Fatalf("expected auto-encrypted archive fetched from gist")
	}
	restoredDB := filepath.Join(tmp, "from-gist", "panel.db")
	if _, err := RestoreArchive(ctx, fetched, pass, restoredDB, ""); err != nil {
		t.Fatalf("RestoreArchive from gist failed: %v", err)
	}
	members := queryMembers(t, restoredDB)
	if members["alice"] != "tok-alice" || members["bob"] != "tok-bob" {
		t.Errorf("restored gist DB members mismatch: %+v", members)
	}
}
