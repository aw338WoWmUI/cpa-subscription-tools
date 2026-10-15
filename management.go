package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

//go:embed ui.html
var page []byte

func (s *service) manage(req pluginapi.ManagementRequest, callback string) pluginapi.ManagementResponse {
	if strings.HasPrefix(req.Path, "/v0/resource/") {
		if req.Method != "GET" || !strings.HasSuffix(req.Path, "/status") {
			return failure(404, "not found")
		}
		return pluginapi.ManagementResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}, "Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"}}, Body: page}
	}
	path := strings.TrimPrefix(req.Path, "/v0/management/plugins/"+pluginID)
	switch {
	case req.Method == "GET" && path == "/accounts":
		b, err := s.host("host.auth.list", map[string]any{})
		if err != nil {
			return failure(502, "could not list accounts")
		}
		var list struct {
			Files []map[string]any `json:"files"`
		}
		if json.Unmarshal(b, &list) != nil {
			return failure(502, "invalid account list")
		}
		files := []map[string]any{}
		for _, a := range list.Files {
			out := map[string]any{}
			for _, key := range []string{"auth_index", "name", "provider", "disabled", "priority", "weight"} {
				if v, ok := a[key]; ok {
					out[key] = v
				}
			}
			files = append(files, out)
		}
		return response(200, map[string]any{"files": files})
	case req.Method == "POST" && (path == "/import" || path == "/import/preview"):
		var body struct {
			Provider string          `json:"provider"`
			Content  json.RawMessage `json:"content"`
			Confirm  bool            `json:"confirm"`
		}
		if json.Unmarshal(req.Body, &body) != nil {
			return failure(400, "invalid import request")
		}
		if path == "/import" && !body.Confirm {
			return failure(400, "import confirmation required")
		}
		v, err := s.importCredential(body.Provider, body.Content, path == "/import")
		if err != nil {
			return failure(400, err.Error())
		}
		return response(200, v)
	case req.Method == "GET" && path == "/credits":
		m, err := s.credential(req.Query.Get("auth_index"), "codex")
		if err != nil {
			return failure(400, err.Error())
		}
		v, err := s.credits(m, callback)
		if err != nil {
			return failure(502, err.Error())
		}
		return response(200, v)
	case req.Method == "POST" && path == "/reset":
		var r resetRequest
		if json.Unmarshal(req.Body, &r) != nil {
			return failure(400, "invalid reset request")
		}
		v, err := s.reset(r, callback)
		if err != nil {
			return failure(400, err.Error())
		}
		return response(200, v)
	case req.Method == "GET" && path == "/copilot-quota":
		m, err := s.credential(req.Query.Get("auth_index"), "copilot")
		if err != nil {
			return failure(400, err.Error())
		}
		b, _ := json.Marshal(m)
		v, err := s.copilotQuota(b, callback)
		if err != nil {
			return failure(502, err.Error())
		}
		return response(200, v)
	case req.Method == "GET" && path == "/pacing":
		s.mu.Lock()
		v := s.last
		s.mu.Unlock()
		return response(200, v)
	case (req.Method == "GET" || req.Method == "PATCH") && path == "/routing":
		s.mu.Lock()
		c := s.config
		s.mu.Unlock()
		var body any
		if req.Method == "PATCH" {
			var r struct {
				Strategy string `json:"strategy"`
				Affinity bool   `json:"session-affinity"`
				TTL      string `json:"session-affinity-ttl"`
			}
			if json.Unmarshal(req.Body, &r) != nil {
				return failure(400, "invalid routing settings")
			}
			d, err := time.ParseDuration(r.TTL)
			if err != nil || d < time.Minute || d > 30*24*time.Hour {
				return failure(400, "affinity lifetime must be between 1m and 720h")
			}
			if r.Strategy != "weighted-round-robin" && r.Strategy != "round-robin" && r.Strategy != "fill-first" {
				return failure(400, "invalid routing strategy")
			}
			body = r
		}
		var out any
		if err := localRequest(context.Background(), c, req.Method, "/v8/management/config/routing", body, &out); err != nil {
			return failure(502, err.Error())
		}
		return response(200, out)
	case req.Method == "POST" && path == "/pacing/run":
		s.mu.Lock()
		c := s.config
		s.mu.Unlock()
		if !c.PacingEnabled {
			return failure(409, "enable pacing in plugin settings first")
		}
		return response(200, s.pace(context.Background(), c))
	default:
		return failure(404, "not found")
	}
}
