package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	sqliteHeaderMagic = "SQLite format 3\x00"
	maxArchiveFileSz  = 512 << 20 // 512 MiB per file safety cap
)

// ExportOptions controls how a panel backup archive is generated.
type ExportOptions struct {
	DBPath     string
	CertsDir   string
	Passphrase string // optional; if non-empty, encrypts tar.gz with Argon2id + AES-256-GCM ("SBOXENC1" + 16B salt + 12B nonce + ciphertext)
}

// InspectResult summarizes the contents of a restored or inspected backup archive.
type InspectResult struct {
	Encrypted bool      `json:"encrypted"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	FileCount int       `json:"file_count"`
	HasDB     bool      `json:"has_db"`
	CertFiles int       `json:"cert_files"`
}

type archiveManifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	HasDB     bool      `json:"has_db"`
	CertFiles int       `json:"cert_files"`
	FileCount int       `json:"file_count"`
}

type collectedCertFile struct {
	tarName string
	srcPath string
	mode    int64
	size    int64
}

// CreateArchive takes an atomic WAL-safe SQLite snapshot via VACUUM INTO, packages panel.db,
// manifest.json, and all regular files under CertsDir into a tar.gz archive, and optionally
// encrypts the result with Argon2id + AES-256-GCM when opts.Passphrase is non-empty.
func CreateArchive(ctx context.Context, opts ExportOptions) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dbPath := strings.TrimSpace(opts.DBPath)
	if dbPath == "" {
		return nil, errors.New("backup: db path is required")
	}
	if st, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("backup: stat db %q: %w", dbPath, err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("backup: db path %q is a directory", dbPath)
	}

	tmpDir, err := os.MkdirTemp("", "sbox-backup-export-*")
	if err != nil {
		return nil, fmt.Errorf("backup: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	snapPath := filepath.Join(tmpDir, "panel.db")
	if err := snapshotSQLite(ctx, dbPath, snapPath); err != nil {
		return nil, err
	}

	dbBytes, err := os.ReadFile(snapPath)
	if err != nil {
		return nil, fmt.Errorf("backup: read snapshot db: %w", err)
	}
	if !hasValidSQLiteHeader(dbBytes) {
		return nil, errors.New("backup: snapshot database has invalid SQLite header")
	}

	certFiles, err := collectCertFiles(strings.TrimSpace(opts.CertsDir))
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	manifest := archiveManifest{
		Version:   1,
		CreatedAt: now,
		HasDB:     true,
		CertFiles: len(certFiles),
		FileCount: 1 + len(certFiles), // panel.db + cert files
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: marshal manifest: %w", err)
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	if err := writeTarBytes(tw, "manifest.json", manifestBytes, 0o600, now); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, err
	}
	if err := writeTarBytes(tw, "panel.db", dbBytes, 0o600, now); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, err
	}

	for _, cf := range certFiles {
		if err := ctx.Err(); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return nil, err
		}
		raw, err := os.ReadFile(cf.srcPath)
		if err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return nil, fmt.Errorf("backup: read cert file %q: %w", cf.srcPath, err)
		}
		if err := writeTarBytes(tw, cf.tarName, raw, cf.mode, now); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return nil, fmt.Errorf("backup: close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("backup: close gzip writer: %w", err)
	}

	payload := buf.Bytes()
	if opts.Passphrase != "" {
		return encryptPayload(payload, opts.Passphrase)
	}
	return payload, nil
}

// RestoreArchive validates, optionally decrypts, and atomically restores panel.db and certs/
// from a backup archive while backing up existing targets to .pre-restore siblings.
func RestoreArchive(ctx context.Context, data []byte, passphrase, targetDBPath, targetCertsDir string) (*InspectResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(data) == 0 {
		return nil, errors.New("backup: archive data is empty")
	}
	targetDBPath = strings.TrimSpace(targetDBPath)
	if targetDBPath == "" {
		return nil, errors.New("backup: target db path is required")
	}
	targetCertsDir = strings.TrimSpace(targetCertsDir)

	encrypted := IsEncrypted(data)
	plainData, err := decryptPayload(data, passphrase)
	if err != nil {
		return nil, err
	}

	gr, err := gzip.NewReader(bytes.NewReader(plainData))
	if err != nil {
		return nil, fmt.Errorf("backup: invalid gzip archive: %w", err)
	}
	defer gr.Close()

	tmpDir, err := os.MkdirTemp("", "sbox-backup-restore-*")
	if err != nil {
		return nil, fmt.Errorf("backup: create restore staging dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	stagingCertsDir := filepath.Join(tmpDir, "certs")
	var stagedDBPath string
	var createdAt time.Time
	var hasDB bool
	var certFiles int
	var fileCount int

	tr := tar.NewReader(gr)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: read tar entry: %w", err)
		}

		cleanName, err := sanitizeTarEntryName(hdr.Name)
		if err != nil {
			return nil, err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("backup: archive contains forbidden link entry %q", hdr.Name)
		case tar.TypeReg, tar.TypeRegA:
			// allowed
		default:
			continue
		}

		if hdr.Size < 0 || hdr.Size > maxArchiveFileSz {
			return nil, fmt.Errorf("backup: archive entry %q has invalid size %d", cleanName, hdr.Size)
		}

		content, err := io.ReadAll(io.LimitReader(tr, maxArchiveFileSz+1))
		if err != nil {
			return nil, fmt.Errorf("backup: read entry %q: %w", cleanName, err)
		}
		if int64(len(content)) > maxArchiveFileSz {
			return nil, fmt.Errorf("backup: archive entry %q exceeds maximum allowed size", cleanName)
		}

		switch {
		case cleanName == "manifest.json":
			var m archiveManifest
			if err := json.Unmarshal(content, &m); err == nil {
				createdAt = m.CreatedAt
			}
		case cleanName == "panel.db":
			if !hasValidSQLiteHeader(content) {
				return nil, errors.New("backup: panel.db in archive does not have a valid SQLite format 3 header")
			}
			stagedDBPath = filepath.Join(tmpDir, "panel.db")
			if err := os.WriteFile(stagedDBPath, content, 0o600); err != nil {
				return nil, fmt.Errorf("backup: stage panel.db: %w", err)
			}
			hasDB = true
			fileCount++
		case strings.HasPrefix(cleanName, "certs/"):
			relCert := strings.TrimPrefix(cleanName, "certs/")
			if relCert == "" {
				continue
			}
			destPath := filepath.Join(stagingCertsDir, filepath.FromSlash(relCert))
			if err := ensureWithinDir(stagingCertsDir, destPath); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
				return nil, fmt.Errorf("backup: mkdir for staged cert %q: %w", relCert, err)
			}
			mode := fs.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o600
			}
			if err := os.WriteFile(destPath, content, mode); err != nil {
				return nil, fmt.Errorf("backup: stage cert %q: %w", relCert, err)
			}
			certFiles++
			fileCount++
		default:
			// Ensure unknown entries still pass staging boundary validation.
			destPath := filepath.Join(tmpDir, filepath.FromSlash(cleanName))
			if err := ensureWithinDir(tmpDir, destPath); err != nil {
				return nil, err
			}
			fileCount++
		}
	}

	if !hasDB || stagedDBPath == "" {
		return nil, errors.New("backup: archive does not contain panel.db")
	}

	// Atomically swap targetDBPath (backing up previous file to .pre-restore and removing stale -wal/-shm).
	if err := replaceDBFile(stagedDBPath, targetDBPath); err != nil {
		return nil, err
	}

	// Atomically swap targetCertsDir if requested.
	if targetCertsDir != "" {
		if err := replaceCertsDir(stagingCertsDir, targetCertsDir, certFiles > 0); err != nil {
			return nil, err
		}
	}

	return &InspectResult{
		Encrypted: encrypted,
		CreatedAt: createdAt,
		FileCount: fileCount,
		HasDB:     true,
		CertFiles: certFiles,
	}, nil
}

func snapshotSQLite(ctx context.Context, srcDBPath, destPath string) error {
	db, err := sql.Open("sqlite", srcDBPath)
	if err != nil {
		return fmt.Errorf("backup: open sqlite %q: %w", srcDBPath, err)
	}
	db.SetMaxOpenConns(1)

	_, execErr := db.ExecContext(ctx, "VACUUM INTO ?", destPath)
	closeErr := db.Close()
	if execErr != nil {
		return fmt.Errorf("backup: sqlite VACUUM INTO snapshot failed: %w", execErr)
	}
	if closeErr != nil {
		return fmt.Errorf("backup: close sqlite after snapshot: %w", closeErr)
	}
	return nil
}

func collectCertFiles(certsDir string) ([]collectedCertFile, error) {
	if certsDir == "" {
		return nil, nil
	}
	info, err := os.Stat(certsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: stat certs dir %q: %w", certsDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("backup: certs path %q is not a directory", certsDir)
	}

	var files []collectedCertFile
	err = filepath.WalkDir(certsDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(certsDir, p)
		if err != nil {
			return err
		}
		tarName := path.Join("certs", filepath.ToSlash(rel))
		mode := int64(fi.Mode().Perm())
		if mode == 0 {
			mode = 0o600
		}
		files = append(files, collectedCertFile{
			tarName: tarName,
			srcPath: p,
			mode:    mode,
			size:    fi.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup: walk certs dir %q: %w", certsDir, err)
	}
	return files, nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mode int64, modTime time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: modTime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: write tar header %q: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("backup: write tar payload %q: %w", name, err)
	}
	return nil
}

func hasValidSQLiteHeader(b []byte) bool {
	return len(b) >= len(sqliteHeaderMagic) && bytes.Equal(b[:len(sqliteHeaderMagic)], []byte(sqliteHeaderMagic))
}

func sanitizeTarEntryName(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("backup: tar entry has empty name")
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("backup: tar entry %q contains NUL byte", raw)
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") || filepath.IsAbs(raw) {
		return "", fmt.Errorf("backup: tar entry %q uses absolute path (path traversal rejected)", raw)
	}
	if len(raw) >= 2 && raw[1] == ':' {
		return "", fmt.Errorf("backup: tar entry %q uses drive-prefixed path (path traversal rejected)", raw)
	}

	normalized := strings.ReplaceAll(raw, "\\", "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", fmt.Errorf("backup: tar entry %q contains path traversal segment '..'", raw)
		}
	}

	cleaned := path.Clean(normalized)
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("backup: tar entry %q resolves outside archive root", raw)
	}
	return cleaned, nil
}

func ensureWithinDir(baseDir, targetPath string) error {
	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		return fmt.Errorf("backup: path boundary check failed: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("backup: path %q escapes target directory", targetPath)
	}
	return nil
}

func replaceDBFile(stagedDBPath, targetDBPath string) error {
	dbParent := filepath.Dir(targetDBPath)
	if err := os.MkdirAll(dbParent, 0o700); err != nil {
		return fmt.Errorf("backup: create db parent dir: %w", err)
	}

	restoringPath := targetDBPath + ".restoring"
	if err := copyFileAtomic(stagedDBPath, restoringPath, 0o600); err != nil {
		return fmt.Errorf("backup: prepare restoring db: %w", err)
	}

	// Backup existing DB to .pre-restore sibling if present.
	if _, err := os.Stat(targetDBPath); err == nil {
		preRestorePath := targetDBPath + ".pre-restore"
		_ = os.Remove(preRestorePath)
		if err := os.Rename(targetDBPath, preRestorePath); err != nil {
			_ = os.Remove(restoringPath)
			return fmt.Errorf("backup: backup existing db to .pre-restore: %w", err)
		}
	}

	// Remove stale SQLite WAL/SHM sidecar files before swapping in the restored DB.
	_ = os.Remove(targetDBPath + "-wal")
	_ = os.Remove(targetDBPath + "-shm")

	if err := os.Rename(restoringPath, targetDBPath); err != nil {
		return fmt.Errorf("backup: replace target db: %w", err)
	}
	return nil
}

func replaceCertsDir(stagingCertsDir, targetCertsDir string, hasStagedCerts bool) error {
	parent := filepath.Dir(targetCertsDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("backup: create certs parent dir: %w", err)
	}

	restoringDir := targetCertsDir + ".restoring"
	_ = os.RemoveAll(restoringDir)
	if err := os.MkdirAll(restoringDir, 0o700); err != nil {
		return fmt.Errorf("backup: create certs restoring dir: %w", err)
	}

	if hasStagedCerts {
		if err := copyDirTree(stagingCertsDir, restoringDir); err != nil {
			_ = os.RemoveAll(restoringDir)
			return fmt.Errorf("backup: stage certs dir: %w", err)
		}
	}

	if _, err := os.Stat(targetCertsDir); err == nil {
		preRestoreDir := targetCertsDir + ".pre-restore"
		_ = os.RemoveAll(preRestoreDir)
		if err := os.Rename(targetCertsDir, preRestoreDir); err != nil {
			_ = os.RemoveAll(restoringDir)
			return fmt.Errorf("backup: backup existing certs dir to .pre-restore: %w", err)
		}
	}

	if err := os.Rename(restoringDir, targetCertsDir); err != nil {
		return fmt.Errorf("backup: replace target certs dir: %w", err)
	}
	return nil
}

func copyFileAtomic(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

func copyDirTree(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o700)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		mode := fi.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
		return copyFileAtomic(p, dstPath, mode)
	})
}
