package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func (s *service) upstream(method, url string, headers http.Header, body []byte, callback string) ([]byte, error) {
	b, err := s.host("host.http.do", map[string]any{"host_callback_id": callback, "request": pluginapi.HTTPRequest{Method: method, URL: url, Headers: headers, Body: body}})
	if err != nil {
		return nil, errors.New("upstream request failed")
	}
	var resp pluginapi.HTTPResponse
	if json.Unmarshal(b, &resp) != nil {
		return nil, errors.New("invalid upstream response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}
func (s *service) credential(index, provider string) (map[string]any, error) {
	if index == "" || len(index) > 256 {
		return nil, errors.New("choose an account")
	}
	b, err := s.host("host.auth.get_runtime", map[string]string{"auth_index": index})
	if err != nil {
		return nil, err
	}
	var runtime struct {
		Auth struct {
			Provider string `json:"provider"`
			Disabled bool   `json:"disabled"`
		}
	}
	if json.Unmarshal(b, &runtime) != nil || runtime.Auth.Provider != provider || runtime.Auth.Disabled {
		return nil, errors.New("unsupported or disabled account")
	}
	b, err = s.host("host.auth.get", map[string]string{"auth_index": index})
	if err != nil {
		return nil, err
	}
	var r struct {
		JSON map[string]any `json:"json"`
	}
	if json.Unmarshal(b, &r) != nil || r.JSON == nil {
		return nil, errors.New("credential unavailable")
	}
	return r.JSON, nil
}
func (s *service) copilotQuota(raw []byte, callback string) (pluginapi.QuotaFetchResponse, error) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return pluginapi.QuotaFetchResponse{}, errors.New("credential unavailable")
	}
	token := firstString(m, "github_access_token")
	origin := firstString(m, "github_base_url")
	if token == "" || (origin != "" && origin != "https://github.com") {
		return pluginapi.QuotaFetchResponse{}, errors.New("public GitHub credential required")
	}
	b, err := s.upstream("GET", "https://api.github.com/copilot_internal/user", http.Header{"Authorization": {"token " + token}, "Accept": {"application/json"}, "User-Agent": {"CLIProxyAPI-Subscription-Tools/" + version}}, nil, callback)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	var d struct {
		Plan   string `json:"copilot_plan"`
		Reset  string `json:"quota_reset_date"`
		Quotas map[string]struct {
			Remaining float64 `json:"percent_remaining"`
			Unlimited bool    `json:"unlimited"`
		} `json:"quota_snapshots"`
	}
	if json.Unmarshal(b, &d) != nil || len(d.Quotas) == 0 {
		return pluginapi.QuotaFetchResponse{}, errors.New("quota data unavailable")
	}
	result := pluginapi.QuotaFetchResponse{Subscription: &pluginapi.QuotaSubscription{Plan: d.Plan}}
	for _, name := range []string{"premium_interactions", "chat", "completions"} {
		q, ok := d.Quotas[name]
		if !ok {
			continue
		}
		v := q.Remaining / 100
		if q.Unlimited {
			v = 1
		}
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		result.Groups = append(result.Groups, pluginapi.QuotaGroup{DisplayName: name, Buckets: []pluginapi.QuotaBucket{{Window: "monthly", RemainingFraction: v, ResetTime: d.Reset}}})
	}
	return result, nil
}

const resetURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"

type credit struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	ResetType string `json:"reset_type"`
}
type credits struct {
	Available int      `json:"available_count"`
	Credits   []credit `json:"credits"`
}

func availableCredit(c credit, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339, c.ExpiresAt)
	return c.ID != "" && c.Status == "available" && c.ResetType == "codex_rate_limits" && err == nil && expires.After(now)
}

func codexHeaders(m map[string]any) (http.Header, error) {
	token := firstString(m, "access_token")
	if token == "" {
		return nil, errors.New("Codex OAuth credential required")
	}
	h := http.Header{"Authorization": {"Bearer " + token}, "User-Agent": {"CLIProxyAPI-Subscription-Tools/" + version}, "Accept": {"application/json"}, "Content-Type": {"application/json"}}
	if id := firstString(m, "account_id"); id != "" {
		h.Set("Chatgpt-Account-Id", id)
	}
	return h, nil
}
func (s *service) credits(m map[string]any, callback string) (credits, error) {
	h, err := codexHeaders(m)
	if err != nil {
		return credits{}, err
	}
	b, err := s.upstream("GET", resetURL, h, nil, callback)
	if err != nil {
		return credits{}, err
	}
	var d credits
	if json.Unmarshal(b, &d) != nil {
		return credits{}, errors.New("invalid Reset Bank response")
	}
	// Keep only the fields needed for an explicit, account-bound manual redemption.
	return d, nil
}

type resetRequest struct {
	AuthIndex string `json:"auth_index"`
	CreditID  string `json:"credit_id"`
	RequestID string `json:"request_id"`
	Confirm   bool   `json:"confirm"`
}
type resetResult struct {
	AuthIndex string `json:"-"`
	CreditID  string `json:"-"`
	Success   bool   `json:"success"`
}

var requestIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func (s *service) reset(req resetRequest, callback string) (resetResult, error) {
	if !req.Confirm || !requestIDPattern.MatchString(req.RequestID) || req.CreditID == "" {
		return resetResult{}, errors.New("confirmation, credit ID and UUID request ID are required")
	}
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	if old, ok := s.resets[req.RequestID]; ok {
		if old.AuthIndex != req.AuthIndex || old.CreditID != req.CreditID {
			return resetResult{}, errors.New("request ID belongs to another operation")
		}
		return old, nil
	}
	m, err := s.credential(req.AuthIndex, "codex")
	if err != nil {
		return resetResult{}, err
	}
	d, err := s.credits(m, callback)
	if err != nil {
		return resetResult{}, err
	}
	found := false
	for _, c := range d.Credits {
		if c.ID == req.CreditID && availableCredit(c, time.Now()) {
			found = true
		}
	}
	if !found {
		return resetResult{}, errors.New("selected reset credit is no longer available; refresh first")
	}
	h, err := codexHeaders(m)
	if err != nil {
		return resetResult{}, err
	}
	body, _ := json.Marshal(map[string]string{"credit_id": req.CreditID, "redeem_request_id": req.RequestID})
	resultBody, err := s.upstream("POST", resetURL+"/consume", h, body, callback)
	if err != nil {
		return resetResult{}, err
	}
	var ack map[string]any
	if len(resultBody) > 0 && (json.Unmarshal(resultBody, &ack) != nil || ack == nil) {
		return resetResult{}, errors.New("reset outcome is uncertain; refresh inventory before retrying")
	}
	if ack["success"] == false || ack["error"] != nil {
		return resetResult{}, errors.New("reset was not confirmed by the account service")
	}
	after, err := s.credits(m, callback)
	if err != nil {
		return resetResult{}, errors.New("reset submitted; refresh inventory to verify the outcome")
	}
	for _, c := range after.Credits {
		if c.ID == req.CreditID && c.Status == "available" {
			return resetResult{}, errors.New("reset submitted but the selected credit is still available; refresh before retrying")
		}
	}
	if after.Available != d.Available-1 {
		return resetResult{}, errors.New("reset submitted but the credit count could not be verified; refresh before retrying")
	}
	result := resetResult{AuthIndex: req.AuthIndex, CreditID: req.CreditID, Success: true}
	if s.resets == nil {
		s.resets = map[string]resetResult{}
	}
	if len(s.resets) >= 1024 {
		clear(s.resets)
	}
	s.resets[req.RequestID] = result
	return result, nil
}
