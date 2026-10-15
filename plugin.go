package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const pluginID = "cpa-subscription-tools"

var version = "0.1.0"

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}
type hostFunc func(string, any) (json.RawMessage, error)
type service struct {
	host     hostFunc
	mu       sync.Mutex
	importMu sync.Mutex
	resetMu  sync.Mutex
	resets   map[string]resetResult
	cancel   func()
	done     chan struct{}
	config   config
	last     pacingResult
}

var app = &service{host: nativeHostCall, resets: map[string]resetResult{}}

type config struct {
	PacingEnabled    bool   `yaml:"pacing_enabled"`
	ManagementURL    string `yaml:"management_url"`
	ManagementKeyEnv string `yaml:"management_key_env"`
	IntervalSeconds  int    `yaml:"interval_seconds"`
}

func main() {}
func registration() any {
	return struct {
		SchemaVersion int                `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]bool    `json:"capabilities"`
	}{6, pluginapi.Metadata{Name: pluginID, Version: version, Author: "aw338WoWmUI", GitHubRepository: "https://github.com/aw338WoWmUI/cpa-subscription-tools", ConfigFields: []pluginapi.ConfigField{
		{Name: "pacing_enabled", Type: "boolean", Default: false, Description: "Apply Quota Pacer dry-run plans through native routing-field updates. Leave Quota Pacer auto_apply off."},
		{Name: "management_url", Type: "string", Default: "http://127.0.0.1:8317", Description: "Loopback host origin used for routing-field updates."},
		{Name: "management_key_env", Type: "string", Default: "MANAGEMENT_PASSWORD", Description: "Environment variable containing the host management key; never put the key in plugin settings."},
		{Name: "interval_seconds", Type: "integer", Default: 60, Description: "Pacing check interval, from 60 to 3600 seconds."},
	}}, map[string]bool{"management_api": true, "quota_provider": true}}
}
func dispatch(method string, raw []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid lifecycle request")
		}
		c := config{ManagementURL: "http://127.0.0.1:8317", ManagementKeyEnv: "MANAGEMENT_PASSWORD", IntervalSeconds: 60}
		if err := yaml.Unmarshal(req.ConfigYAML, &c); err != nil {
			return nil, errors.New("invalid plugin settings")
		}
		if err := app.configure(c); err != nil {
			return nil, err
		}
		return registration(), nil
	case "plugin.quiesce", "plugin.shutdown":
		app.stop()
		return struct{}{}, nil
	case "management.register":
		routes := []map[string]string{}
		for _, r := range [][2]string{{"GET", "/accounts"}, {"POST", "/import/preview"}, {"POST", "/import"}, {"GET", "/credits"}, {"POST", "/reset"}, {"GET", "/copilot-quota"}, {"GET", "/routing"}, {"PATCH", "/routing"}, {"GET", "/pacing"}, {"POST", "/pacing/run"}} {
			routes = append(routes, map[string]string{"Method": r[0], "Path": "/v0/management/plugins/" + pluginID + r[1]})
		}
		return map[string]any{"routes": routes, "resources": []map[string]string{{"Path": "/status", "Menu": "Subscription Tools", "Description": "Native credential import, Copilot quota, manual Reset Bank and safe quota pacing."}}}, nil
	case "management.handle":
		var req struct {
			pluginapi.ManagementRequest
			CallbackID string `json:"host_callback_id"`
		}
		if json.Unmarshal(raw, &req) != nil {
			return nil, errors.New("invalid management request")
		}
		return app.manage(req.ManagementRequest, req.CallbackID), nil
	case "quota.identifier":
		return "subscription-tools", nil
	case "quota.describe":
		return pluginapi.QuotaDescribeResponse{SupportedProviders: []string{"copilot"}, DisplayName: "GitHub Copilot"}, nil
	case "quota.fetch":
		var req struct {
			pluginapi.QuotaFetchRequest
			CallbackID string `json:"host_callback_id"`
		}
		if json.Unmarshal(raw, &req) != nil {
			return nil, errors.New("invalid quota request")
		}
		if req.Provider != "copilot" {
			return nil, errors.New("unsupported quota provider")
		}
		return app.copilotQuota(req.StorageJSON, req.CallbackID)
	case "quota.reset":
		return nil, errors.New("Copilot quota cannot be reset")
	default:
		return nil, errors.New("unsupported plugin method")
	}
}
func response(code int, v any) pluginapi.ManagementResponse {
	b, _ := json.Marshal(v)
	return pluginapi.ManagementResponse{StatusCode: code, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: b}
}
func failure(code int, msg string) pluginapi.ManagementResponse {
	return response(code, map[string]string{"error": msg})
}
