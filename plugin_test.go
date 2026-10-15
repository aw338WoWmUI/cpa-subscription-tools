package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func TestImportNormalizesNativeCredentialsWithoutExposingTokens(t *testing.T) {
	for _, tc := range []struct{ provider, input string }{{"claude", `{"mcpOAuth":{"private":"ignore"},"claudeAiOauth":{"accessToken":"access-secret","refreshToken":"refresh-secret","expiresAt":1893456000000}}`}, {"codex", `{"tokens":{"access_token":"access-secret","refresh_token":"refresh-secret","account_id":"account"}}`}} {
		t.Run(tc.provider, func(t *testing.T) {
			saved := 0
			s := service{host: func(method string, payload any) (json.RawMessage, error) {
				if method == "host.auth.list" {
					return raw(map[string]any{"files": []any{}}), nil
				}
				if method != "host.auth.save" {
					t.Fatalf("unexpected %s", method)
				}
				m := payload.(map[string]any)
				if strings.Contains(string(m["json"].(json.RawMessage)), "mcpOAuth") {
					t.Fatal("unrelated credentials included")
				}
				var auth map[string]any
				json.Unmarshal(m["json"].(json.RawMessage), &auth)
				if auth["type"] != tc.provider || auth["refresh_token"] != "refresh-secret" {
					t.Fatal("normalization failed")
				}
				saved++
				return raw(map[string]any{"name": m["name"]}), nil
			}}
			preview, err := s.importCredential(tc.provider, []byte(tc.input), false)
			if err != nil || saved != 0 {
				t.Fatal(err)
			}
			if strings.Contains(string(raw(preview)), "secret") {
				t.Fatal("secret in preview")
			}
			if _, err = s.importCredential(tc.provider, []byte(tc.input), true); err != nil || saved != 1 {
				t.Fatal(err)
			}
		})
	}
}
func TestImportRejectsDuplicateAndUnconfirmedWrites(t *testing.T) {
	m, _ := normalize("claude", []byte(`{"refresh_token":"refresh"}`))
	name := credentialName("claude", m)
	calls := 0
	s := service{host: func(method string, _ any) (json.RawMessage, error) {
		calls++
		if method != "host.auth.list" {
			t.Fatal("overwrite attempted")
		}
		return raw(map[string]any{"files": []any{map[string]any{"name": name}}}), nil
	}}
	if _, err := s.importCredential("claude", []byte(`{"refresh_token":"refresh"}`), true); err == nil {
		t.Fatal("duplicate accepted")
	}
	resp := s.manage(pluginapi.ManagementRequest{Method: "POST", Path: "/v0/management/plugins/" + pluginID + "/import", Body: []byte(`{"provider":"claude","content":{"refresh_token":"refresh"}}`)}, "")
	if resp.StatusCode != 400 || calls != 1 {
		t.Fatal("confirmation bypass")
	}
}
func TestPublicResourceCannotInvokeManagementActions(t *testing.T) {
	s := service{host: func(string, any) (json.RawMessage, error) {
		t.Fatal("public resource accessed credentials")
		return nil, nil
	}}
	for _, path := range []string{"/v0/resource/plugins/" + pluginID + "/status", "/v0/resource/plugins/" + pluginID + "/accounts", "/v0/resource/plugins/" + pluginID + "/reset"} {
		r := s.manage(pluginapi.ManagementRequest{Method: "GET", Path: path}, "")
		if strings.HasSuffix(path, "/status") && r.StatusCode != 200 {
			t.Fatal("missing static page")
		}
	}
}
func resetHost(t *testing.T, consumed *int) hostFunc {
	return func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case "host.auth.get_runtime":
			return raw(map[string]any{"auth": map[string]any{"provider": "codex"}}), nil
		case "host.auth.get":
			return raw(map[string]any{"json": map[string]any{"access_token": "secret", "account_id": "account"}}), nil
		case "host.http.do":
			r := payload.(map[string]any)["request"].(pluginapi.HTTPRequest)
			if r.Headers.Get("Authorization") != "Bearer secret" {
				t.Fatal("missing credential")
			}
			body := raw(map[string]any{"available_count": 1, "credits": []credit{{ID: "credit", Status: "available", ResetType: "codex_rate_limits", ExpiresAt: "2030-01-01T00:00:00Z"}}})
			if *consumed > 0 {
				body = raw(map[string]any{"available_count": 0, "credits": []credit{}})
			}
			if r.Method == "POST" {
				var b map[string]string
				json.Unmarshal(r.Body, &b)
				if b["credit_id"] != "credit" || b["redeem_request_id"] == "" {
					t.Fatal("missing targeted/idempotent request")
				}
				*consumed++
				body = []byte(`{"success":true}`)
			}
			return raw(pluginapi.HTTPResponse{StatusCode: 200, Body: body}), nil
		}
		t.Fatal("unexpected callback")
		return nil, nil
	}
}

func TestResetRejectsExpiredOrAmbiguousCreditsAndBusinessFailure(t *testing.T) {
	for _, expires := range []string{"2000-01-01T00:00:00Z", "", "invalid"} {
		if availableCredit(credit{ID: "c", Status: "available", ResetType: "codex_rate_limits", ExpiresAt: expires}, time.Now()) {
			t.Fatal("unavailable credit accepted")
		}
	}
	consumed := 0
	base := resetHost(t, &consumed)
	s := service{host: func(method string, payload any) (json.RawMessage, error) {
		if method == "host.http.do" && payload.(map[string]any)["request"].(pluginapi.HTTPRequest).Method == "POST" {
			return raw(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"success":false}`)}), nil
		}
		return base(method, payload)
	}, resets: map[string]resetResult{}}
	r := resetRequest{AuthIndex: "account", CreditID: "credit", RequestID: "4a52f39e-f016-4ed4-8080-846f6e717acc", Confirm: true}
	if _, err := s.reset(r, ""); err == nil || len(s.resets) != 0 {
		t.Fatal("business failure cached as success")
	}
}
func TestResetRequiresConfirmationAndReplaysOnlyOnce(t *testing.T) {
	consumed := 0
	s := service{host: resetHost(t, &consumed), resets: map[string]resetResult{}}
	r := resetRequest{AuthIndex: "account", CreditID: "credit", RequestID: "4a52f39e-f016-4ed4-8080-846f6e717acc"}
	if _, err := s.reset(r, ""); err == nil || consumed != 0 {
		t.Fatal("unconfirmed reset")
	}
	r.Confirm = true
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.reset(r, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if consumed != 1 {
		t.Fatalf("consumed %d credits", consumed)
	}
	r.AuthIndex = "other"
	if _, err := s.reset(r, ""); err == nil {
		t.Fatal("request ID reused across accounts")
	}
}
func TestCopilotQuotaUsesProviderOwnedTokenAndHostTransport(t *testing.T) {
	s := service{host: func(method string, payload any) (json.RawMessage, error) {
		if method != "host.http.do" {
			t.Fatal("unexpected callback")
		}
		r := payload.(map[string]any)["request"].(pluginapi.HTTPRequest)
		if r.URL != "https://api.github.com/copilot_internal/user" || r.Headers.Get("Authorization") != "token github-secret" {
			t.Fatal("incorrect Copilot token/origin")
		}
		return raw(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"copilot_plan":"individual","quota_snapshots":{"premium_interactions":{"percent_remaining":42}}}`)}), nil
	}}
	q, err := s.copilotQuota([]byte(`{"github_access_token":"github-secret"}`), "scope")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Groups) != 1 || q.Groups[0].Buckets[0].RemainingFraction != .42 || strings.Contains(string(raw(q)), "secret") {
		t.Fatal("quota normalization failed")
	}
}
func TestPacingUsesFreshPlansAndNativeFieldPatches(t *testing.T) {
	patched := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing local authentication")
		}
		switch r.URL.Path {
		case "/v8/management/config":
			fmt.Fprint(w, `{"routing":{"strategy":"weighted-round-robin"},"plugins":{"configs":{"quota-pacer":{"auto_apply":false}}}}`)
		case "/v0/management/plugins/quota-pacer/run":
			if r.URL.Query().Get("mode") != "dry-run" {
				t.Error("unsafe apply")
			}
			fmt.Fprint(w, `{"attempted":0,"snapshot":{"items":[{"auth_index":"healthy","provider":"codex","evidence_fresh":true,"status":"active","target":{"priority":100,"weight":812}},{"auth_index":"depleted","provider":"claude","evidence_fresh":true,"status":"active","reason":"fresh remaining depleted","target":{"priority":0}},{"auth_index":"stale","provider":"codex","evidence_fresh":false,"status":"active","target":{"priority":100,"weight":5}}]}}`)
		case "/v8/management/credentials":
			fmt.Fprint(w, `{"files":[{"name":"healthy.json","auth_index":"healthy"},{"name":"depleted.json","auth_index":"depleted"},{"name":"stale.json","auth_index":"stale"}]}`)
		case "/v8/management/credentials/fields":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			name := p["name"]
			if name == "stale.json" {
				t.Fatal("stale evidence applied")
			}
			if name == "depleted.json" && p["weight"] != float64(0) {
				t.Error("depleted credential not excluded")
			}
			for k := range p {
				if k != "name" && k != "priority" && k != "weight" {
					t.Error("unrelated credential field patched")
				}
			}
			patched++
			fmt.Fprint(w, `{}`)
		default:
			t.Error("unexpected route")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("TEST_MANAGEMENT_KEY", "test-key")
	c := config{ManagementURL: server.URL, ManagementKeyEnv: "TEST_MANAGEMENT_KEY", IntervalSeconds: 60}
	var result pacingResult
	if err := applyPacing(context.Background(), c, &result); err != nil {
		t.Fatal(err)
	}
	if patched != 2 || result.Planned != 2 || result.Changed != 2 {
		t.Fatalf("unexpected patches: %+v %d", result, patched)
	}
}
func TestPacingRejectsUnsafeHostOrMissingKey(t *testing.T) {
	s := service{}
	for _, url := range []string{"http://evil.example", "http://127.0.0.1:8317/path", "http://u:p@127.0.0.1:8317", "https://127.0.0.1:8317"} {
		if s.configure(config{ManagementURL: url, ManagementKeyEnv: "TEST_KEY", IntervalSeconds: 60}) == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	if err := s.configure(config{ManagementURL: "http://127.0.0.1:8317", ManagementKeyEnv: "MISSING_SUBSCRIPTION_TEST_KEY", IntervalSeconds: 60, PacingEnabled: true}); err == nil {
		t.Fatal("missing management key accepted")
	}
}

func TestRoutingUpdatesOnlySelectedFieldsAndValidatesBeforeWriting(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/v8/management/config/routing" {
			t.Error("unexpected routing endpoint")
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if len(b) != 3 || b["session-affinity"] != true || b["session-affinity-ttl"] != "24h" {
			t.Error("routing fields lost")
		}
		writes++
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	t.Setenv("TEST_KEY", "key")
	s := service{config: config{ManagementURL: server.URL, ManagementKeyEnv: "TEST_KEY"}}
	req := pluginapi.ManagementRequest{Method: "PATCH", Path: "/v0/management/plugins/" + pluginID + "/routing", Body: []byte(`{"strategy":"weighted-round-robin","session-affinity":true,"session-affinity-ttl":"24h"}`)}
	if r := s.manage(req, ""); r.StatusCode != 200 || writes != 1 {
		t.Fatal("valid routing update failed")
	}
	req.Body = []byte(`{"strategy":"weighted-round-robin","session-affinity":true,"session-affinity-ttl":"invalid"}`)
	if r := s.manage(req, ""); r.StatusCode != 400 || writes != 1 {
		t.Fatal("invalid TTL reached host")
	}
}
