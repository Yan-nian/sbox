package syncer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMergeInstanceBlock_PlainMultiInstance(t *testing.T) {
	manualLine := "trojan://manual-pass@manual.example.com:443#Manual-Node"

	// 1. First VPS appends cleanly after existing manual line.
	step1 := MergeInstanceBlock(manualLine, "jp-tokyo-1", "vless://u1@1.1.1.1:443#JP-1\nhy2://p1@1.1.1.1:8443#JP-2\n", false)
	if !strings.Contains(step1, manualLine) {
		t.Fatalf("manual line lost: %s", step1)
	}
	if !strings.Contains(step1, "# >>> sbox-panel:jp-tokyo-1 >>>") || !strings.Contains(step1, "# <<< sbox-panel:jp-tokyo-1 <<<") {
		t.Fatalf("missing jp-tokyo-1 markers: %s", step1)
	}

	// 2. Second VPS appends its own block without disturbing manual line or jp-tokyo-1.
	step2 := MergeInstanceBlock(step1, "us-la-1", "vless://u2@2.2.2.2:443#US-1", false)
	if !strings.Contains(step2, "vless://u1@1.1.1.1:443#JP-1") || !strings.Contains(step2, "vless://u2@2.2.2.2:443#US-1") {
		t.Fatalf("expected both VPS blocks preserved: %s", step2)
	}

	// 3. First VPS updates its nodes — ONLY jp-tokyo-1 block changes; manual & us-la-1 untouched.
	step3 := MergeInstanceBlock(step2, "jp-tokyo-1", "vless://u1-new@1.1.1.9:443#JP-New", false)
	if strings.Contains(step3, "vless://u1@1.1.1.1:443#JP-1") || strings.Contains(step3, "hy2://p1@1.1.1.1:8443#JP-2") {
		t.Fatalf("stale jp-tokyo-1 lines should be replaced: %s", step3)
	}
	if !strings.Contains(step3, "vless://u1-new@1.1.1.9:443#JP-New") {
		t.Fatalf("missing updated jp-tokyo-1 line: %s", step3)
	}
	if !strings.Contains(step3, manualLine) || !strings.Contains(step3, "vless://u2@2.2.2.2:443#US-1") {
		t.Fatalf("manual or us-la-1 block lost: %s", step3)
	}

	// 4. Idempotency: applying the same update again yields byte-identical output.
	step4 := MergeInstanceBlock(step3, "jp-tokyo-1", "vless://u1-new@1.1.1.9:443#JP-New", false)
	if step4 != step3 {
		t.Fatalf("MergeInstanceBlock not idempotent:\n--- got ---\n%s\n--- want ---\n%s", step4, step3)
	}

	// 5. Merged output must parse cleanly via ParseSubscription (comments ignored).
	parsed, err := ParseSubscription([]byte(step3), FetchOptions{})
	if err != nil {
		t.Fatalf("ParseSubscription on merged plain URI failed: %v", err)
	}
	if parsed.Format != "uri" || len(parsed.Nodes) != 3 {
		t.Fatalf("expected 3 parsed nodes from merged output, got format=%q count=%d", parsed.Format, len(parsed.Nodes))
	}
}

func TestMergeInstanceBlock_Base64MultiInstance(t *testing.T) {
	// Existing content starts as Base64 with another instance's block.
	existingPlain := strings.Join([]string{
		"# >>> sbox-panel:sg-1 >>>",
		"trojan://sgpass@3.3.3.3:443#SG-1",
		"# <<< sbox-panel:sg-1 <<<",
	}, "\n")
	existingB64 := base64.StdEncoding.EncodeToString([]byte(existingPlain))
	newJPB64 := base64.StdEncoding.EncodeToString([]byte("vless://jpuuid@4.4.4.4:443#JP-1"))

	mergedB64 := MergeInstanceBlock(existingB64, "jp-1", newJPB64, true)
	decodedBytes, err := base64.StdEncoding.DecodeString(mergedB64)
	if err != nil {
		t.Fatalf("merged Base64 is not valid StdEncoding base64: %v", err)
	}
	decoded := string(decodedBytes)
	if !strings.Contains(decoded, "trojan://sgpass@3.3.3.3:443#SG-1") || !strings.Contains(decoded, "vless://jpuuid@4.4.4.4:443#JP-1") {
		t.Fatalf("unexpected decoded base64 merge:\n%s", decoded)
	}

	// Updating sg-1 must preserve jp-1 and remain valid Base64.
	updatedSGB64 := base64.StdEncoding.EncodeToString([]byte("trojan://sgpass2@3.3.3.9:443#SG-Updated"))
	merged2B64 := MergeInstanceBlock(mergedB64, "sg-1", updatedSGB64, true)
	decoded2Bytes, err := base64.StdEncoding.DecodeString(merged2B64)
	if err != nil {
		t.Fatalf("second merged Base64 invalid: %v", err)
	}
	decoded2 := string(decoded2Bytes)
	if strings.Contains(decoded2, "SG-1") || !strings.Contains(decoded2, "SG-Updated") || !strings.Contains(decoded2, "JP-1") {
		t.Fatalf("unexpected second base64 merge:\n%s", decoded2)
	}

	// Plain existing content fallback when isBase64 is true should still merge cleanly into Base64.
	fromPlainB64 := MergeInstanceBlock("trojan://plain@5.5.5.5:443#Manual", "hk-1", newJPB64, true)
	fromPlainDec, err := base64.StdEncoding.DecodeString(fromPlainB64)
	if err != nil {
		t.Fatalf("fallback merge not valid base64: %v", err)
	}
	if !strings.Contains(string(fromPlainDec), "Manual") || !strings.Contains(string(fromPlainDec), "JP-1") {
		t.Fatalf("fallback base64 merge lost lines:\n%s", string(fromPlainDec))
	}

	// Merged Base64 must parse directly via ParseSubscription.
	parsed, err := ParseSubscription([]byte(merged2B64), FetchOptions{})
	if err != nil {
		t.Fatalf("ParseSubscription on merged base64 failed: %v", err)
	}
	if parsed.Format != "uri" || len(parsed.Nodes) != 2 {
		t.Fatalf("expected 2 nodes from merged base64, got format=%q count=%d", parsed.Format, len(parsed.Nodes))
	}
}

func TestPushToTarget_GitHubGist(t *testing.T) {
	var mu sync.Mutex
	gistContent := "# >>> sbox-panel:other-vps >>>\ntrojan://other@9.9.9.9:443#Other\n# <<< sbox-panel:other-vps <<<"
	truncatedMode := false
	var lastAuth, lastAccept string
	var getCalls, patchCalls int

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		lastAuth = r.Header.Get("Authorization")
		lastAccept = r.Header.Get("Accept")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gists/gist-123":
			getCalls++
			fileObj := map[string]any{
				"content":   gistContent,
				"truncated": truncatedMode,
				"raw_url":   srv.URL + "/raw/gist-123/sbox-nodes.txt",
			}
			if truncatedMode {
				fileObj["content"] = "truncated..."
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "https://gist.github.com/user/gist-123",
				"files": map[string]any{
					"sbox-nodes.txt": fileObj,
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/raw/gist-123/sbox-nodes.txt":
			_, _ = w.Write([]byte(gistContent))

		case r.Method == http.MethodPatch && r.URL.Path == "/gists/gist-123":
			patchCalls++
			var body struct {
				Files map[string]struct {
					Content string `json:"content"`
				} `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			gistContent = body.Files["sbox-nodes.txt"].Content
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "https://gist.github.com/user/gist-123",
				"files": map[string]any{
					"sbox-nodes.txt": map[string]any{
						"content": gistContent,
						"raw_url": "https://gist.githubusercontent.com/user/gist-123/raw/sbox-nodes.txt",
					},
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// 1. merge_by_instance with truncated raw_url fallback.
	truncatedMode = true
	res := PushToTarget(context.Background(), PushTargetConfig{
		Type:       "github_gist",
		Format:     "uri",
		MergeMode:  "merge_by_instance",
		InstanceID: "jp-1",
		Token:      "ghp_secret",
		GistID:     "gist-123",
		FilePath:   "sbox-nodes.txt",
		APIBaseURL: srv.URL,
	}, "vless://u1@1.1.1.1:443#JP-1", 1)

	if !res.Success || res.HTTPStatus != http.StatusOK {
		t.Fatalf("expected gist push success, got %+v", res)
	}
	if lastAuth != "Bearer ghp_secret" || lastAccept != "application/vnd.github+json" {
		t.Fatalf("unexpected GitHub headers: auth=%q accept=%q", lastAuth, lastAccept)
	}
	if res.RemoteURL != "https://gist.githubusercontent.com/user/gist-123/raw/sbox-nodes.txt" {
		t.Fatalf("unexpected RemoteURL: %q", res.RemoteURL)
	}
	if !strings.Contains(gistContent, "trojan://other@9.9.9.9:443#Other") || !strings.Contains(gistContent, "vless://u1@1.1.1.1:443#JP-1") {
		t.Fatalf("merged gist content missing expected nodes:\n%s", gistContent)
	}
	if getCalls != 1 || patchCalls != 1 {
		t.Fatalf("expected 1 GET + 1 PATCH, got GET=%d PATCH=%d", getCalls, patchCalls)
	}

	// 2. Clash format with merge_by_instance should NOT apply URI block markers — cleanly replaces!
	res2 := PushToTarget(context.Background(), PushTargetConfig{
		Type:       "github_gist",
		Format:     "clash",
		MergeMode:  "merge_by_instance",
		InstanceID: "jp-1",
		Token:      "ghp_secret",
		GistID:     "gist-123",
		FilePath:   "sbox-nodes.txt",
		APIBaseURL: srv.URL,
	}, "proxies:\n  - name: JP-1\n", 1)
	if !res2.Success {
		t.Fatalf("expected clash gist push success, got %+v", res2)
	}
	if strings.Contains(gistContent, "# >>> sbox-panel:") || gistContent != "proxies:\n  - name: JP-1\n" {
		t.Fatalf("clash format must cleanly replace without URI markers, got:\n%s", gistContent)
	}
	if getCalls != 1 {
		t.Fatalf("expected no GET when merge is skipped for clash format, got getCalls=%d", getCalls)
	}
}

func TestPushToTarget_GitHubRepo_CreateUpdateAnd409Retry(t *testing.T) {
	var mu sync.Mutex
	exists := false
	currentSHA := ""
	currentPlain := ""
	triggerConflictOnce := false
	putAttempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/repos/alice/subs/contents/nodes/all.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("ref") != "main" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			// Simulate GitHub's line-wrapped base64 content response.
			b64 := base64.StdEncoding.EncodeToString([]byte(currentPlain))
			if len(b64) > 20 {
				b64 = b64[:20] + "\n" + b64[20:]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":      currentSHA,
				"encoding": "base64",
				"content":  b64,
			})

		case http.MethodPut:
			putAttempts++
			var req struct {
				Message string `json:"message"`
				Content string `json:"content"`
				Branch  string `json:"branch"`
				SHA     string `json:"sha"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if triggerConflictOnce {
				triggerConflictOnce = false
				// Simulate concurrent push by another VPS changing SHA & adding its block!
				currentSHA = "sha-v3-concurrent"
				currentPlain = MergeInstanceBlock(currentPlain, "eu-de", "trojan://eu@8.8.8.8:443#EU-1", false)
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"does not match expected sha"}`))
				return
			}
			if exists && req.SHA != currentSHA {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"sha mismatch"}`))
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(req.Content)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			exists = true
			currentPlain = string(decoded)
			if currentSHA == "" {
				currentSHA = "sha-v1"
				w.WriteHeader(http.StatusCreated)
			} else {
				currentSHA = "sha-next"
				w.WriteHeader(http.StatusOK)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": currentSHA}})
		}
	}))
	defer srv.Close()

	baseCfg := PushTargetConfig{
		Type:       "github_repo",
		Format:     "uri",
		MergeMode:  "merge_by_instance",
		InstanceID: "jp-1",
		Token:      "ghp_repo_token",
		Repo:       "alice/subs",
		Branch:     "main",
		FilePath:   "/nodes/all.txt",
		APIBaseURL: srv.URL,
	}

	// 1. Create new file on 404.
	resCreate := PushToTarget(context.Background(), baseCfg, "vless://jp1@1.1.1.1:443#JP-1", 1)
	if !resCreate.Success || resCreate.HTTPStatus != http.StatusCreated {
		t.Fatalf("expected 201 create success, got %+v", resCreate)
	}
	wantURL := "https://raw.githubusercontent.com/alice/subs/main/nodes/all.txt"
	if resCreate.RemoteURL != wantURL {
		t.Fatalf("RemoteURL=%q, want %q", resCreate.RemoteURL, wantURL)
	}
	if !strings.Contains(currentPlain, "vless://jp1@1.1.1.1:443#JP-1") {
		t.Fatalf("unexpected created content:\n%s", currentPlain)
	}

	// 2. Update with 409 Conflict — must automatically re-fetch, preserve concurrent eu-de block, and succeed!
	putAttempts = 0
	triggerConflictOnce = true
	resRetry := PushToTarget(context.Background(), baseCfg, "vless://jp2@1.1.1.2:443#JP-2", 1)
	if !resRetry.Success || resRetry.HTTPStatus != http.StatusOK {
		t.Fatalf("expected 409 retry to succeed, got %+v", resRetry)
	}
	if putAttempts != 2 {
		t.Fatalf("expected exactly 2 PUT attempts (conflict + retry), got %d", putAttempts)
	}
	if !strings.Contains(currentPlain, "trojan://eu@8.8.8.8:443#EU-1") || !strings.Contains(currentPlain, "vless://jp2@1.1.1.2:443#JP-2") {
		t.Fatalf("expected both concurrent eu-de and updated jp-1 blocks after 409 retry:\n%s", currentPlain)
	}
	if strings.Contains(currentPlain, "JP-1") {
		t.Fatalf("old jp-1 node should be replaced:\n%s", currentPlain)
	}
}

func TestPushToTarget_SubStore_CreateAndPatch(t *testing.T) {
	var mu sync.Mutex
	subs := map[string]map[string]any{}
	var postCount, patchCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		const prefix = "/secret-key"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		subPath := strings.TrimPrefix(r.URL.Path, prefix)

		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(subPath, "/api/sub/"):
			name := strings.TrimPrefix(subPath, "/api/sub/")
			if item, ok := subs[name]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": item})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "failed", "error": map[string]any{"message": "not found"}})

		case r.Method == http.MethodPost && subPath == "/api/subs":
			postCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			name, _ := body["name"].(string)
			subs[name] = body
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": body})

		case r.Method == http.MethodPatch && strings.HasPrefix(subPath, "/api/sub/"):
			patchCount++
			name := strings.TrimPrefix(subPath, "/api/sub/")
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			subs[name] = body
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": body})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := PushTargetConfig{
		Type:        "sub_store",
		Format:      "base64",
		InstanceID:  "jp-tokyo/1",
		SubStoreURL: srv.URL + "/secret-key/",
	}

	plainURIs := "vless://u1@1.1.1.1:443#JP-1\nhy2://p1@1.1.1.1:8443#JP-2"
	b64Content := base64.StdEncoding.EncodeToString([]byte(plainURIs))

	// 1. Not found (404) -> POST /api/subs, decoding base64 to plain URI lines!
	res1 := PushToTarget(context.Background(), cfg, b64Content, 2)
	if !res1.Success || res1.HTTPStatus != http.StatusCreated {
		t.Fatalf("expected sub_store create success, got %+v", res1)
	}
	if postCount != 1 || patchCount != 0 {
		t.Fatalf("expected 1 POST and 0 PATCH, got POST=%d PATCH=%d", postCount, patchCount)
	}
	wantRemote := srv.URL + "/secret-key/download/sbox-jp-tokyo-1"
	if res1.RemoteURL != wantRemote {
		t.Fatalf("RemoteURL=%q, want %q", res1.RemoteURL, wantRemote)
	}
	stored := subs["sbox-jp-tokyo-1"]
	if stored == nil || stored["source"] != "local" || stored["content"] != plainURIs {
		t.Fatalf("expected decoded plain URI content stored in Sub-Store, got: %#v", stored)
	}

	// 2. Exists (200) -> PATCH /api/sub/{name}
	updatedPlain := "vless://u2@2.2.2.2:443#JP-New"
	res2 := PushToTarget(context.Background(), cfg, base64.StdEncoding.EncodeToString([]byte(updatedPlain)), 1)
	if !res2.Success || res2.HTTPStatus != http.StatusOK {
		t.Fatalf("expected sub_store patch success, got %+v", res2)
	}
	if postCount != 1 || patchCount != 1 {
		t.Fatalf("expected 1 POST and 1 PATCH, got POST=%d PATCH=%d", postCount, patchCount)
	}
	if subs["sbox-jp-tokyo-1"]["content"] != updatedPlain {
		t.Fatalf("expected updated content, got: %#v", subs["sbox-jp-tokyo-1"])
	}

	// 3. Verify 200 with explicit {"status":"success","data":null} is treated as not found -> POST create.
	nullSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"status":"success","data":null}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/subs":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer nullSrv.Close()

	res3 := PushToTarget(context.Background(), PushTargetConfig{
		Type:         "sub_store",
		Format:       "uri",
		SubStoreURL:  nullSrv.URL,
		SubStoreName: "custom-sub",
	}, updatedPlain, 1)
	if !res3.Success || res3.HTTPStatus != http.StatusCreated {
		t.Fatalf("expected POST create when GET returns data:null, got %+v", res3)
	}
}

func TestPushToTarget_ValidationErrors(t *testing.T) {
	if res := PushToTarget(context.Background(), PushTargetConfig{Type: "unknown"}, "abc", 1); res.Success {
		t.Fatalf("expected error for unknown type")
	}
	if res := PushToTarget(context.Background(), PushTargetConfig{Type: "github_gist"}, "abc", 1); res.Success {
		t.Fatalf("expected error for missing gist token")
	}
	if res := PushToTarget(context.Background(), PushTargetConfig{Type: "github_repo", Token: "t", Repo: "invalid"}, "abc", 1); res.Success {
		t.Fatalf("expected error for invalid repo format")
	}
	if res := PushToTarget(context.Background(), PushTargetConfig{Type: "sub_store", SubStoreURL: "ftp://bad"}, "abc", 1); res.Success {
		t.Fatalf("expected error for invalid sub_store_url")
	}
}
