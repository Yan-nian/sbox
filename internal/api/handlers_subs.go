package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/subgen"
)

func normalizeSubFormat(raw string, allowAuto bool) (string, bool) {
	f := strings.ToLower(strings.TrimSpace(raw))
	switch f {
	case "", "auto":
		if allowAuto {
			return "auto", true
		}
		return "base64", true
	case "base64", "b64":
		return "base64", true
	case "uri", "plain", "text":
		return "uri", true
	case "singbox", "sing-box", "json":
		return "singbox", true
	case "clash", "mihomo", "yaml":
		return "clash", true
	default:
		return "", false
	}
}

func memberBoundToInbound(ib model.Inbound, memberID int64) bool {
	if ib.MemberMode != "selected" {
		return true
	}
	for _, id := range ib.MemberIDs {
		if id == memberID {
			return true
		}
	}
	return false
}

// extractNonLoopbackHost returns the HTTP request hostname (stripped of port) if it is
// a usable non-loopback host or IP, otherwise returns empty string.
func extractNonLoopbackHost(r *http.Request) string {
	candidates := []string{
		strings.TrimSpace(r.Header.Get("X-Forwarded-Host")),
		strings.TrimSpace(r.Host),
	}
	for _, raw := range candidates {
		if raw == "" {
			continue
		}
		if idx := strings.Index(raw, ","); idx >= 0 {
			raw = strings.TrimSpace(raw[:idx])
		}
		host := raw
		if h, _, err := net.SplitHostPort(raw); err == nil {
			host = h
		}
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" || strings.EqualFold(host, "localhost") {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			if ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
		}
		return host
	}
	return ""
}

func (s *Server) renderMemberSubscription(ctx context.Context, member *model.Member, format, fallbackLocalHost string) (*subgen.RenderOutput, error) {
	ps, _ := s.store.GetPanelSettings(ctx)
	instanceID := strings.TrimSpace(ps.InstanceID)
	if instanceID == "" {
		instanceID = "sbox-panel"
	}

	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	subNodes := make([]subgen.NodeEndpoint, 0, len(nodes))
	for _, n := range nodes {
		v4 := strings.TrimSpace(n.PublicIPv4)
		v6 := strings.TrimSpace(n.PublicIPv6)
		if n.IsLocal {
			if v4 == "" {
				v4 = strings.TrimSpace(ps.PublicIPv4)
			}
			if v6 == "" {
				v6 = strings.TrimSpace(ps.PublicIPv6)
			}
			if v4 == "" && v6 == "" && fallbackLocalHost != "" {
				if ip := net.ParseIP(fallbackLocalHost); ip != nil && ip.To4() == nil {
					v6 = fallbackLocalHost
				} else {
					v4 = fallbackLocalHost
				}
			}
		}
		subNodes = append(subNodes, subgen.NodeEndpoint{
			NodeID:     n.ID,
			NodeName:   n.Name,
			PublicIPv4: v4,
			PublicIPv6: v6,
		})
	}

	certs, err := s.store.ListCertificates(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	certByID := make(map[int64]model.Certificate, len(certs))
	for _, c := range certs {
		certByID[c.ID] = c
	}

	inbounds, err := s.store.ListInbounds(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list inbounds: %w", err)
	}
	subInbounds := make([]subgen.InboundEntry, 0, len(inbounds))
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		if !memberBoundToInbound(ib, member.ID) {
			continue
		}

		extras := make([]subgen.ExtraEntry, 0, len(ib.ExtraEntries))
		for _, ex := range ib.ExtraEntries {
			extras = append(extras, subgen.ExtraEntry{
				Name: ex.Name,
				Host: ex.Host,
				Port: ex.Port,
			})
		}

		entry := subgen.InboundEntry{
			ID:           ib.ID,
			NodeID:       ib.NodeID,
			Name:         ib.Name,
			Tag:          ib.Tag,
			Protocol:     ib.Protocol,
			ListenPort:   ib.ListenPort,
			Settings:     ib.Settings,
			ExternalHost: ib.ExternalHost,
			ExternalPort: ib.ExternalPort,
			ExtraEntries: extras,
		}
		if ib.CertificateID != nil && *ib.CertificateID > 0 {
			if c, ok := certByID[*ib.CertificateID]; ok {
				entry.CertSelfSigned = c.Type == "self_signed"
				entry.CertFingerprint = c.Fingerprint
				entry.CertDomains = c.Domains
			}
		}
		subInbounds = append(subInbounds, entry)
	}

	out, err := subgen.Render(subgen.RenderInput{
		InstanceID: instanceID,
		Member: subgen.MemberCred{
			ID:          member.ID,
			Name:        member.Name,
			UUID:        member.UUID,
			Password:    member.Password,
			SS2022Key16: member.SS2022Key16,
			SS2022Key32: member.SS2022Key32,
			SubToken:    member.SubToken,
		},
		Nodes:    subNodes,
		Inbounds: subInbounds,
	}, format)
	if err != nil {
		return nil, err
	}
	if out.Nodes == nil {
		out.Nodes = []subgen.RenderedNode{}
	}
	return out, nil
}

// handlePublicSubscription serves GET {web_base_path}/sub/{token} authenticated strictly by member.sub_token.
func (s *Server) handlePublicSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		s.serveNotFoundOrDecoy(w, r)
		return
	}

	member, err := s.store.GetMemberBySubToken(ctx, token)
	if err != nil || !member.Enabled {
		s.serveNotFoundOrDecoy(w, r)
		return
	}

	rawFmt := r.URL.Query().Get("format")
	format, ok := normalizeSubFormat(rawFmt, true)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported format: use auto, base64, uri, singbox, or clash")
		return
	}
	if format == "auto" {
		format = subgen.DetectFormatFromUA(r.UserAgent())
	}

	out, err := s.renderMemberSubscription(ctx, member, format, extractNonLoopbackHost(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render subscription: "+err.Error())
		return
	}

	contentType := out.ContentType
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	filename := out.Filename
	if filename == "" {
		filename = fmt.Sprintf("sbox-%s.txt", member.Name)
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=0; expire=0", member.UploadBytes, member.DownloadBytes))
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(out.Content))
	}
}

// handleSubscriptionPreview serves GET /api/v1/subscriptions/preview?member_id=<id>&format=...
func (s *Server) handleSubscriptionPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("member_id")), 10, 64)
	if err != nil || memberID <= 0 {
		writeError(w, http.StatusBadRequest, "valid member_id query parameter is required")
		return
	}

	member, err := s.store.GetMember(ctx, memberID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}

	rawFmt := strings.TrimSpace(r.URL.Query().Get("format"))
	urlFmt := rawFmt
	renderFmt := ""
	if rawFmt == "" || strings.EqualFold(rawFmt, "auto") {
		renderFmt = "base64"
		if strings.EqualFold(rawFmt, "auto") {
			urlFmt = "auto"
		} else {
			urlFmt = "base64"
		}
	} else {
		norm, ok := normalizeSubFormat(rawFmt, false)
		if !ok {
			writeError(w, http.StatusBadRequest, "unsupported format: use base64, uri, singbox, or clash")
			return
		}
		renderFmt = norm
		urlFmt = norm
	}

	out, err := s.renderMemberSubscription(ctx, member, renderFmt, extractNonLoopbackHost(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render subscription preview: "+err.Error())
		return
	}

	subURL := fmt.Sprintf("%s/sub/%s?format=%s", s.BasePath(), member.SubToken, urlFmt)
	writeJSON(w, http.StatusOK, map[string]any{
		"format":  out.Format,
		"sub_url": subURL,
		"content": out.Content,
		"nodes":   out.Nodes,
	})
}

type inboundLinkItem struct {
	MemberID   int64  `json:"member_id"`
	MemberName string `json:"member_name"`
	Name       string `json:"name"`
	URI        string `json:"uri"`
	Loon       string `json:"loon"`
}

// handleInboundLinks serves GET /api/v1/inbounds/{id}/links returning per-member standard URI and native Loon lines.
func (s *Server) handleInboundLinks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid inbound id")
		return
	}

	ib, err := s.store.GetInbound(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "inbound not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load inbound")
		return
	}

	items := make([]inboundLinkItem, 0)
	if strings.EqualFold(strings.TrimSpace(ib.Protocol), "direct") {
		writeJSON(w, http.StatusOK, map[string]any{
			"inbound_id":   ib.ID,
			"inbound_name": ib.Name,
			"protocol":     ib.Protocol,
			"items":        items,
		})
		return
	}

	ps, _ := s.store.GetPanelSettings(ctx)
	instanceID := strings.TrimSpace(ps.InstanceID)
	if instanceID == "" {
		instanceID = "sbox-panel"
	}

	node, err := s.store.GetNode(ctx, ib.NodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load inbound node")
		return
	}

	v4 := strings.TrimSpace(node.PublicIPv4)
	v6 := strings.TrimSpace(node.PublicIPv6)
	if node.IsLocal {
		if v4 == "" {
			v4 = strings.TrimSpace(ps.PublicIPv4)
		}
		if v6 == "" {
			v6 = strings.TrimSpace(ps.PublicIPv6)
		}
		if v4 == "" && v6 == "" {
			if fallbackHost := extractNonLoopbackHost(r); fallbackHost != "" {
				if ip := net.ParseIP(fallbackHost); ip != nil && ip.To4() == nil {
					v6 = fallbackHost
				} else {
					v4 = fallbackHost
				}
			}
		}
	}

	nodeEp := subgen.NodeEndpoint{
		NodeID:     node.ID,
		NodeName:   node.Name,
		PublicIPv4: v4,
		PublicIPv6: v6,
	}

	extras := make([]subgen.ExtraEntry, 0, len(ib.ExtraEntries))
	for _, ex := range ib.ExtraEntries {
		extras = append(extras, subgen.ExtraEntry{
			Name: ex.Name,
			Host: ex.Host,
			Port: ex.Port,
		})
	}
	ibEntry := subgen.InboundEntry{
		ID:           ib.ID,
		NodeID:       ib.NodeID,
		Name:         ib.Name,
		Tag:          ib.Tag,
		Protocol:     ib.Protocol,
		ListenPort:   ib.ListenPort,
		Settings:     ib.Settings,
		ExternalHost: ib.ExternalHost,
		ExternalPort: ib.ExternalPort,
		ExtraEntries: extras,
	}
	if ib.CertificateID != nil && *ib.CertificateID > 0 {
		if c, err := s.store.GetCertificate(ctx, *ib.CertificateID); err == nil {
			ibEntry.CertSelfSigned = c.Type == "self_signed"
			ibEntry.CertFingerprint = c.Fingerprint
			ibEntry.CertDomains = c.Domains
		}
	}

	members, err := s.store.ListMembers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}

	for _, m := range members {
		if !m.Enabled || !memberBoundToInbound(*ib, m.ID) {
			continue
		}
		out, renderErr := subgen.Render(subgen.RenderInput{
			InstanceID: instanceID,
			Member: subgen.MemberCred{
				ID:          m.ID,
				Name:        m.Name,
				UUID:        m.UUID,
				Password:    m.Password,
				SS2022Key16: m.SS2022Key16,
				SS2022Key32: m.SS2022Key32,
				SubToken:    m.SubToken,
			},
			Nodes:    []subgen.NodeEndpoint{nodeEp},
			Inbounds: []subgen.InboundEntry{ibEntry},
		}, "uri")
		if renderErr != nil {
			continue
		}
		for _, rn := range out.Nodes {
			items = append(items, inboundLinkItem{
				MemberID:   m.ID,
				MemberName: m.Name,
				Name:       rn.Name,
				URI:        rn.URI,
				Loon:       rn.Loon,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"inbound_id":   ib.ID,
		"inbound_name": ib.Name,
		"protocol":     ib.Protocol,
		"items":        items,
	})
}
