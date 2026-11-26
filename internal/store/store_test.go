package store

import (
	"context"
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

	// Verify all 9 blueprint tables + schema_migrations exist
	tables, err := st.TableNames(ctx)
	if err != nil {
		t.Fatalf("TableNames: %v", err)
	}
	expected := []string{
		"certificates",
		"inbounds",
		"members",
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

	// Second call must preserve existing values
	bp2, id2, secret2, err := st.EnsureBootstrapDefaults(ctx, 19000)
	if err != nil {
		t.Fatalf("second EnsureBootstrapDefaults: %v", err)
	}
	if bp1 != bp2 || id1 != id2 || secret1 != secret2 {
		t.Fatalf("bootstrap values changed across calls: (%s,%s) vs (%s,%s)", bp1, id1, bp2, id2)
	}
}

func TestStoreCRUDCertificatesMembersInboundsAndTraffic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Certificate CRUD
	notBefore := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	notAfter := time.Now().UTC().Add(365 * 24 * time.Hour).Truncate(time.Second)
	certRec := &model.Certificate{
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
	if err := st.RecordTrafficDeltas(ctx, now, []TrafficDeltaInput{
		{InboundTag: "vless-in", MemberName: "alice", Upload: 1000, Download: 2000},
	}); err != nil {
		t.Fatalf("RecordTrafficDeltas 1: %v", err)
	}
	if err := st.RecordTrafficDeltas(ctx, now, []TrafficDeltaInput{
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

	statsResp, err := st.GetTrafficStats(ctx, "24h", 0, 0)
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
	statsAfter, _ := st.GetTrafficStats(ctx, "24h", 0, 0)
	if len(statsAfter.Series) != 0 {
		t.Fatalf("expected empty series after ResetTraffic(all), got %+v", statsAfter.Series)
	}
}
