package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

func TestOpenMigrationsAndBootstrap(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "panel.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Verify WAL mode is active
	var journalMode string
	if err := st.DB().QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected WAL journal_mode, got %q", journalMode)
	}

	// Verify all 9 blueprint tables + nodes + schema_migrations exist
	tables, err := st.TableNames(ctx)
	if err != nil {
		t.Fatalf("TableNames: %v", err)
	}
	expected := []string{
		"certificates",
		"inbounds",
		"members",
		"nodes",
		"outbounds_extra",
		"route_rules",
		"schema_migrations",
		"settings",
		"sync_targets",
		"traffic_stats",
		"upstream_subs",
	}
	have := make(map[string]bool, len(tables))
	for _, name := range tables {
		have[name] = true
	}
	for _, exp := range expected {
		if !have[exp] {
			t.Errorf("missing expected table %q; got tables %v", exp, tables)
		}
	}

	// Idempotent re-migrate
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate should be idempotent: %v", err)
	}

	// Bootstrap defaults
	bp1, id1, secret1, err := st.EnsureBootstrapDefaults(ctx, 18080)
	if err != nil {
		t.Fatalf("EnsureBootstrapDefaults: %v", err)
	}
	if !strings.HasPrefix(bp1, "/sbox-") || len(bp1) < 10 {
		t.Fatalf("unexpected generated web_base_path: %q", bp1)
	}
	if id1 == "" || len(secret1) < 32 {
		t.Fatalf("unexpected instance_id=%q jwt_secret len=%d", id1, len(secret1))
	}

	// Local node id=1 must exist and adopt instance_id
	localNode, err := st.GetNode(ctx, 1)
	if err != nil {
		t.Fatalf("GetNode(1): %v", err)
	}
	if !localNode.IsLocal || localNode.Name != id1 {
		t.Fatalf("unexpected local node: %+v", localNode)
	}

	// Second call must preserve existing values
	bp2, id2, secret2, err := st.EnsureBootstrapDefaults(ctx, 19000)
	if err != nil {
		t.Fatalf("second EnsureBootstrapDefaults: %v", err)
	}
	if bp1 != bp2 || id1 != id2 || secret1 != secret2 {
		t.Fatalf("bootstrap values changed across calls: (%s,%s) vs (%s,%s)", bp1, id1, bp2, id2)
	}
}

func TestMigrateLegacyM2DatabaseZeroDataLoss(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-m2.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}

	ctx := context.Background()
	// Apply strictly Migration 1 and Migration 2 (NO `nodes` table, NO `node_id` columns!)
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
			t.Fatalf("exec legacy migration %d: %v", m.Version, err)
		}
		if _, err := rawDB.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.Version, m.Name); err != nil {
			t.Fatalf("record legacy migration %d: %v", m.Version, err)
		}
	}

	// Seed realistic production M2 data (matching `hw-sg` on sbox-test!)
	seedSQL := `
INSERT INTO settings (key, value) VALUES
	('instance_id', 'hw-sg'),
	('web_base_path', '/sbox-f0a3e2c2'),
	('initialized', 'true'),
	('public_ipv4', '188.239.22.220'),
	('log_level', 'debug'),
	('github_proxy', 'https://ghproxy.example/'),
	('override_patch', '{"log":{"level":"debug"}}');
INSERT INTO certificates (id, name, domain, cert_type, domains_json, cert_path, key_path)
	VALUES (1, 'Legacy Cert', 'edge.example.com', 'self_signed', '["edge.example.com"]', '/data/certs/1/fullchain.pem', '/data/certs/1/privkey.pem');
INSERT INTO members (id, name, enabled, uuid, password, ss2022_key16, ss2022_key32, sub_token, upload_bytes, download_bytes)
	VALUES (1, 'alice', 1, '11111111-2222-3333-4444-555555555555', 'pw', 'k16', 'k32', 'tok-alice', 100, 200);
INSERT INTO inbounds (id, name, tag, protocol, enabled, listen, listen_port, config_json, certificate_id, upload_bytes, download_bytes)
	VALUES (1, 'VLESS Main', 'vless-main', 'vless', 1, '::', 24443, '{"tls_mode":"reality"}', 1, 100, 200);
INSERT INTO traffic_stats (member_id, inbound_id, member_name, inbound_tag, granularity, bucket_time, upload_bytes, download_bytes)
	VALUES (1, 1, 'alice', 'vless-main', 'day', CURRENT_TIMESTAMP, 100, 200);
INSERT INTO upstream_subs (name, url) VALUES ('Upstream A', 'https://example.com/sub');
`
	if _, err := rawDB.ExecContext(ctx, seedSQL); err != nil {
		t.Fatalf("seed legacy m2 data: %v", err)
	}
	_ = rawDB.Close()

	// Upgrade via Milestone 3 store.Open()
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("upgrade M2 -> M3 Open failed: %v", err)
	}
	defer st.Close()

	// 1. Local node id=1 seeded from hw-sg settings
	local, err := st.GetNode(ctx, 1)
	if err != nil {
		t.Fatalf("GetNode(1) after migration: %v", err)
	}
	if !local.IsLocal || local.Name != "hw-sg" || local.PublicIPv4 != "188.239.22.220" ||
		local.LogLevel != "debug" || local.GithubProxy != "https://ghproxy.example/" ||
		local.OverridePatch != `{"log":{"level":"debug"}}` {
		t.Fatalf("local node did not inherit M2 settings cleanly: %+v", local)
	}

	// 2. Existing cert, inbound, member, traffic_stats, upstream_sub preserved with proper node_id
	certs, err := st.ListCertificates(ctx, 1)
	if err != nil || len(certs) != 1 || certs[0].Name != "Legacy Cert" || certs[0].NodeID != 1 {
		t.Fatalf("legacy cert lost or wrong node_id: %+v err=%v", certs, err)
	}
	inbounds, err := st.ListInbounds(ctx, 1)
	if err != nil || len(inbounds) != 1 || inbounds[0].Tag != "vless-main" || inbounds[0].NodeID != 1 || inbounds[0].DownloadBytes != 200 {
		t.Fatalf("legacy inbound lost or wrong node_id: %+v err=%v", inbounds, err)
	}
	subs, err := st.ListUpstreamSubs(ctx, 0)
	if err != nil || len(subs) != 1 || subs[0].NodeID != 0 || subs[0].Tag == "" {
		t.Fatalf("legacy upstream_sub lost or wrong node_id/tag: %+v err=%v", subs, err)
	}

	// 3. Remote node id=2 can create an inbound with the exact same tag ("vless-main") and separate traffic!
	remote := &model.Node{Name: "jp-tokyo", PublicIPv4: "203.0.113.20"}
	if err := st.CreateNode(ctx, remote); err != nil {
		t.Fatalf("CreateNode remote: %v", err)
	}
	if remote.ID != 2 || len(remote.Token) != 64 {
		t.Fatalf("unexpected remote node: %+v", remote)
	}

	in2 := &model.Inbound{
		NodeID:     remote.ID,
		Name:       "Tokyo VLESS",
		Tag:        "vless-main",
		Protocol:   "vless",
		Enabled:    true,
		ListenPort: 24443,
	}
	if err := st.CreateInbound(ctx, in2); err != nil {
		t.Fatalf("CreateInbound same tag on node 2 failed: %v", err)
	}
	if err := st.RecordTrafficDeltas(ctx, remote.ID, time.Now().UTC(), []TrafficDeltaInput{
		{InboundTag: "vless-main", MemberName: "alice", Upload: 500, Download: 900},
	}); err != nil {
		t.Fatalf("RecordTrafficDeltas on node 2: %v", err)
	}

	stats, err := st.GetTrafficStats(ctx, "7d", 0, 0, 0)
	if err != nil {
		t.Fatalf("GetTrafficStats: %v", err)
	}
	if len(stats.ByNode) != 2 {
		t.Fatalf("expected 2 nodes in by_node, got %+v", stats.ByNode)
	}

	// Re-opening migrated DB must be 100% idempotent
	_ = st.Close()
	st2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("re-open migrated DB failed: %v", err)
	}
	defer st2.Close()
}

func TestStoreCRUDCertificatesMembersInboundsNodesAndTraffic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Remote node CRUD + token auth + runtime info + reset + delete
	n2 := &model.Node{Name: "us-west", Note: "LA node"}
	if err := st.CreateNode(ctx, n2); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	byTok, err := st.GetNodeByToken(ctx, n2.Token)
	if err != nil || byTok.ID != n2.ID {
		t.Fatalf("GetNodeByToken failed: %+v err=%v", byTok, err)
	}
	if err := st.UpdateNodeRuntimeInfo(ctx, n2.ID, NodeRuntimeUpdate{
		OS: "linux", Arch: "amd64", Hostname: "la-vps", AgentVersion: "0.3.0", CoreVersion: "1.12.0", DataDir: "/var/lib/sbox-agent", PublicIPv4: "198.51.100.5",
	}); err != nil {
		t.Fatalf("UpdateNodeRuntimeInfo: %v", err)
	}
	updatedN2, _ := st.GetNode(ctx, n2.ID)
	if updatedN2.OS != "linux" || updatedN2.PublicIPv4 != "198.51.100.5" || updatedN2.LastSeenAt == nil {
		t.Fatalf("unexpected updated remote node: %+v", updatedN2)
	}
	oldTok := n2.Token
	newTok, err := st.ResetNodeToken(ctx, n2.ID)
	if err != nil || newTok == oldTok || len(newTok) != 64 {
		t.Fatalf("ResetNodeToken failed: newTok=%q err=%v", newTok, err)
	}
	if err := st.DeleteNode(ctx, 1, true); err == nil {
		t.Fatalf("expected error deleting local master node id=1")
	}

	// Certificate CRUD
	notBefore := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	notAfter := time.Now().UTC().Add(365 * 24 * time.Hour).Truncate(time.Second)
	certRec := &model.Certificate{
		NodeID:       1,
		Name:         "Main Cert",
		Type:         "acme_dns",
		Domains:      []string{"example.com", "*.example.com"},
		ACMEEmail:    "ops@example.com",
		DNSProvider:  "cloudflare",
		DNSCredsJSON: `{"api_token":"secret"}`,
		CertPath:     "/data/certs/1/fullchain.pem",
		KeyPath:      "/data/certs/1/privkey.pem",
		Issuer:       "Let's Encrypt",
		Fingerprint:  "deadbeef",
		NotBefore:    &notBefore,
		NotAfter:     &notAfter,
		AutoRenew:    true,
	}
	if err := st.CreateCertificate(ctx, certRec); err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	if certRec.ID == 0 || !certRec.HasDNSCreds || len(certRec.Domains) != 2 {
		t.Fatalf("unexpected saved cert: %+v", certRec)
	}

	// Settings CRUD including panel_cert_id
	if err := st.SetSetting(ctx, model.SettingGithubProxy, "https://gh.example.com/"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	ps, err := st.GetPanelSettings(ctx)
	if err != nil {
		t.Fatalf("GetPanelSettings: %v", err)
	}
	ps.WebBasePath = "custom-path/"
	ps.ListenPort = 19090
	ps.InstanceID = "jp-tokyo-1"
	ps.PanelCertID = certRec.ID
	if err := st.UpdatePanelSettings(ctx, ps); err != nil {
		t.Fatalf("UpdatePanelSettings: %v", err)
	}
	ps2, err := st.GetPanelSettings(ctx)
	if err != nil {
		t.Fatalf("GetPanelSettings after update: %v", err)
	}
	if ps2.WebBasePath != "/custom-path" || ps2.ListenPort != 19090 || ps2.InstanceID != "jp-tokyo-1" || ps2.PanelCertID != certRec.ID {
		t.Fatalf("unexpected updated settings: %+v", ps2)
	}

	// Member CRUD + Toggle
	m := &model.Member{
		Name:        "alice",
		Enabled:     true,
		UUID:        "11111111-2222-3333-4444-555555555555",
		Password:    "secret-password",
		SS2022Key16: "MTIzNDU2Nzg5MDEyMzQ1Ng==",
		SS2022Key32: "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=",
		SubToken:    "tok-alice-123",
		Note:        "Primary user",
	}
	if err := st.CreateMember(ctx, m); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	toggledM, err := st.ToggleMember(ctx, m.ID)
	if err != nil || toggledM.Enabled {
		t.Fatalf("ToggleMember failed: %+v err=%v", toggledM, err)
	}
	toggledM, err = st.ToggleMember(ctx, m.ID)
	if err != nil || !toggledM.Enabled {
		t.Fatalf("ToggleMember back failed: %+v err=%v", toggledM, err)
	}

	// Inbound CRUD + cert reference check + Toggle
	certID := certRec.ID
	in := &model.Inbound{
		NodeID:        1,
		Name:          "VLESS Reality",
		Tag:           "vless-in",
		Protocol:      "vless",
		Enabled:       true,
		Listen:        "::",
		ListenPort:    24443,
		Settings:      json.RawMessage(`{"tls_mode":"reality"}`),
		CertificateID: &certID,
		MemberMode:    "selected",
		MemberIDs:     []int64{m.ID},
		ExternalHost:  "edge.example.com",
		ExternalPort:  443,
		ExtraEntries:  []model.ExtraEntry{{Name: "CN Relay", Host: "relay.example.com", Port: 20443}},
	}
	if err := st.CreateInbound(ctx, in); err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	refs, err := st.CountInboundsByCertificate(ctx, certID)
	if err != nil || refs != 1 {
		t.Fatalf("expected 1 inbound referencing cert, got %d err=%v", refs, err)
	}
	in.Name = "VLESS Updated"
	if err := st.UpdateInbound(ctx, in); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	toggledIn, err := st.ToggleInbound(ctx, in.ID)
	if err != nil || toggledIn.Enabled {
		t.Fatalf("ToggleInbound failed: %+v err=%v", toggledIn, err)
	}

	// Traffic deltas + GetTrafficStats + ResetTraffic
	now := time.Now().UTC()
	if err := st.RecordTrafficDeltas(ctx, 1, now, []TrafficDeltaInput{
		{InboundTag: "vless-in", MemberName: "alice", Upload: 1000, Download: 2000},
	}); err != nil {
		t.Fatalf("RecordTrafficDeltas 1: %v", err)
	}
	if err := st.RecordTrafficDeltas(ctx, 1, now, []TrafficDeltaInput{
		{InboundTag: "vless-in-detour", MemberName: "alice", Upload: 500, Download: 1500},
	}); err != nil {
		t.Fatalf("RecordTrafficDeltas 2: %v", err)
	}

	gotMember, err := st.GetMember(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if gotMember.UploadBytes != 1500 || gotMember.DownloadBytes != 3500 {
		t.Fatalf("unexpected member traffic: up=%d down=%d", gotMember.UploadBytes, gotMember.DownloadBytes)
	}

	statsResp, err := st.GetTrafficStats(ctx, "24h", 0, 0, 0)
	if err != nil {
		t.Fatalf("GetTrafficStats: %v", err)
	}
	if len(statsResp.Series) != 1 || statsResp.Series[0].UploadBytes != 1500 || statsResp.Series[0].DownloadBytes != 3500 {
		t.Fatalf("unexpected traffic stats series: %+v", statsResp.Series)
	}
	if len(statsResp.ByMember) != 1 || statsResp.ByMember[0].Name != "alice" {
		t.Fatalf("unexpected by_member: %+v", statsResp.ByMember)
	}
	if len(statsResp.ByInbound) != 1 || statsResp.ByInbound[0].Tag != "vless-in" {
		t.Fatalf("unexpected by_inbound: %+v", statsResp.ByInbound)
	}
	if len(statsResp.ByNode) != 2 || statsResp.ByNode[0].DownloadBytes != 3500 {
		t.Fatalf("unexpected by_node: %+v", statsResp.ByNode)
	}

	if err := st.ResetTraffic(ctx, "member", m.ID); err != nil {
		t.Fatalf("ResetTraffic(member): %v", err)
	}
	gotMember2, _ := st.GetMember(ctx, m.ID)
	if gotMember2.UploadBytes != 0 || gotMember2.DownloadBytes != 0 {
		t.Fatalf("expected member traffic reset to 0, got %+v", gotMember2)
	}
	if err := st.ResetTraffic(ctx, "all", 0); err != nil {
		t.Fatalf("ResetTraffic(all): %v", err)
	}
	statsAfter, _ := st.GetTrafficStats(ctx, "24h", 0, 0, 0)
	if len(statsAfter.Series) != 0 {
		t.Fatalf("expected empty series after ResetTraffic(all), got %+v", statsAfter.Series)
	}
}
