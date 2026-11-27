package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/sbox-panel/sbox-panel/internal/store"
)

func (s *Server) handleTrafficStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rangeKey := q.Get("range")
	nodeID := parseNodeIDQuery(r, 0, true)
	var memberID, inboundID int64
	if raw := q.Get("member_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			memberID = v
		}
	}
	if raw := q.Get("inbound_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			inboundID = v
		}
	}

	resp, err := s.store.GetTrafficStats(r.Context(), rangeKey, nodeID, memberID, inboundID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query traffic stats: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTrafficReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope string `json:"scope"`
		ID    int64  `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Scope == "" {
		req.Scope = "all"
	}

	if err := s.store.ResetTraffic(r.Context(), req.Scope, req.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "target record not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
