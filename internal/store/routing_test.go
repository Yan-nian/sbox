package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

func TestMilestone4RoutingUpstreamsOutboundsAndMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "m4.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Create node 2 so we test global (node_id=0) + per-node scoping
	n2 := &model.Node{Name: "sg-edge"}
	if err := st.CreateNode(ctx, n2); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// Default outbound update
	if err := st.SetNodeDefaultOutbound(ctx, 1, "warp"); err != nil {
		t.Fatalf("SetNodeDefaultOutbound: %v", err)
	}
	n1, _ := st.GetNode(ctx, 1)
	if n1.DefaultOutbound != "warp" {
		t.Fatalf("expected default_outbound=warp, got %q", n1.DefaultOutbound)
	}

	// UpstreamSub global (node_id=0) & node-specific (node_id=2)
	uGlobal := &model.UpstreamSub{
		NodeID:          0,
		Name:            "Global Airport",
		URL:             "https://example.com/sub-global",
		Enabled:         true,
		IntervalMinutes: 180,
		EnableURLTest:   true,
	}
	if err := st.CreateUpstreamSub(ctx, uGlobal); err != nil {
		t.Fatalf("CreateUpstreamSub global: %v", err)
	}
	if uGlobal.Tag != "sub-1" {
		t.Fatalf("expected auto tag sub-1, got %q", uGlobal.Tag)
	}

	uNode2 := &model.UpstreamSub{
		NodeID:          n2.ID,
		Name:            "SG Only",
		Tag:             "sg-sub",
		URL:             "https://example.com/sub-sg",
		Enabled:         true,
		IntervalMinutes: 60,
		EnableURLTest:   false,
	}
	if err := st.CreateUpstreamSub(ctx, uNode2); err != nil {
		t.Fatalf("CreateUpstreamSub node2: %v", err)
	}

	// Node 1 sees only global; Node 2 sees global + node 2
	list1, err := st.ListUpstreamSubs(ctx, 1)
	if err != nil || len(list1) != 1 || list1[0].ID != uGlobal.ID {
		t.Fatalf("unexpected node 1 upstreams: %+v err=%v", list1, err)
	}
	list2, err := st.ListUpstreamSubs(ctx, n2.ID)
	if err != nil || len(list2) != 2 {
		t.Fatalf("unexpected node 2 upstreams: %+v err=%v", list2, err)
	}

	// Due upstream check (both never fetched yet)
	due, err := st.ListDueUpstreamSubs(ctx, time.Now().UTC())
	if err != nil || len(due) != 2 {
		t.Fatalf("expected 2 due upstreams, got %d err=%v", len(due), err)
	}
	now := time.Now().UTC()
	uGlobal.LastFetchedAt = &now
	uGlobal.NodeCount = 5
	uGlobal.CachedFormat = "clash"
	if err := st.UpdateUpstreamSub(ctx, uGlobal); err != nil {
		t.Fatalf("UpdateUpstreamSub: %v", err)
	}
	dueAfter, _ := st.ListDueUpstreamSubs(ctx, now.Add(5*time.Minute))
	if len(dueAfter) != 1 || dueAfter[0].ID != uNode2.ID {
		t.Fatalf("expected only uNode2 due, got %+v", dueAfter)
	}

	// Regression check: manual-only upstream (interval_minutes == 0) must persist as 0 and stay excluded from ListDueUpstreamSubs
	uManual := &model.UpstreamSub{
		NodeID:          0,
		Name:            "Manual Only",
		Tag:             "manual-sub",
		URL:             "https://example.com/manual",
		Enabled:         true,
		IntervalMinutes: 0,
	}
	if err := st.CreateUpstreamSub(ctx, uManual); err != nil {
		t.Fatalf("CreateUpstreamSub manual: %v", err)
	}
	if uManual.IntervalMinutes != 0 {
		t.Fatalf("expected interval_minutes=0 preserved on create, got %d", uManual.IntervalMinutes)
	}
	uNode2.IntervalMinutes = 0
	if err := st.UpdateUpstreamSub(ctx, uNode2); err != nil {
		t.Fatalf("UpdateUpstreamSub manual: %v", err)
	}
	if uNode2.IntervalMinutes != 0 {
		t.Fatalf("expected interval_minutes=0 preserved on update, got %d", uNode2.IntervalMinutes)
	}
	dueManualCheck, err := st.ListDueUpstreamSubs(ctx, now.Add(5*time.Minute))
	if err != nil || len(dueManualCheck) != 0 {
		t.Fatalf("expected manual-only upstreams excluded from ListDueUpstreamSubs, got %+v err=%v", dueManualCheck, err)
	}

	// Extra outbounds with per-node tag uniqueness (same tag "warp" allowed on node 1 and node 2!)
	ob1 := &model.OutboundExtra{
		NodeID:   1,
		Name:     "Cloudflare WARP",
		Tag:      "warp",
		Type:     "warp",
		Enabled:  true,
		Settings: json.RawMessage(`{"endpoint_host":"engage.cloudflareclient.com","endpoint_port":2408}`),
	}
	if err := st.CreateOutboundExtra(ctx, ob1); err != nil {
		t.Fatalf("CreateOutboundExtra node 1: %v", err)
	}
	ob2 := &model.OutboundExtra{
		NodeID:   n2.ID,
		Name:     "Cloudflare WARP SG",
		Tag:      "warp",
		Type:     "warp",
		Enabled:  true,
		Settings: json.RawMessage(`{"endpoint_host":"engage.cloudflareclient.com","endpoint_port":2408}`),
	}
	if err := st.CreateOutboundExtra(ctx, ob2); err != nil {
		t.Fatalf("CreateOutboundExtra same tag on node 2 should succeed: %v", err)
	}
	toggledOb, err := st.ToggleOutboundExtra(ctx, ob1.ID)
	if err != nil || toggledOb.Enabled {
		t.Fatalf("ToggleOutboundExtra failed: %+v err=%v", toggledOb, err)
	}

	// Route rules CRUD, preset lookup, and ReorderRouteRules
	r1 := &model.RouteRule{
		NodeID:      1,
		Name:        "AI Unlock",
		PresetKey:   "ai_services",
		Enabled:     true,
		Action:      "route",
		OutboundTag: "warp",
		RuleSets:    []string{"geosite-openai"},
	}
	if err := st.CreateRouteRule(ctx, r1); err != nil {
		t.Fatalf("CreateRouteRule r1: %v", err)
	}
	r2 := &model.RouteRule{
		NodeID:    1,
		Name:      "Block BT",
		PresetKey: "block_bt",
		Enabled:   true,
		Action:    "reject",
		Protocols: []string{"bittorrent"},
		Ports:     []int{6881},
	}
	if err := st.CreateRouteRule(ctx, r2); err != nil {
		t.Fatalf("CreateRouteRule r2: %v", err)
	}
	foundPreset, err := st.FindRouteRuleByPresetKey(ctx, 1, "ai_services")
	if err != nil || foundPreset.ID != r1.ID {
		t.Fatalf("FindRouteRuleByPresetKey failed: %+v err=%v", foundPreset, err)
	}

	// Reverse order: r2 before r1
	if err := st.ReorderRouteRules(ctx, 1, []int64{r2.ID, r1.ID}); err != nil {
		t.Fatalf("ReorderRouteRules: %v", err)
	}
	rules, err := st.ListRouteRules(ctx, 1)
	if err != nil || len(rules) != 2 || rules[0].ID != r2.ID || rules[1].ID != r1.ID {
		t.Fatalf("unexpected reordered rules: %+v err=%v", rules, err)
	}
}
