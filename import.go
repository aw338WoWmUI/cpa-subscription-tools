package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func normalize(provider string, raw json.RawMessage) (map[string]any, error) {
	var content map[string]any
	if len(raw) > 1<<20 || json.Unmarshal(raw, &content) != nil || content == nil {
		return nil, errors.New("invalid credential JSON")
	}
	out := map[string]any{"type": provider}
	switch provider {
	case "claude":
		if v, ok := content["claudeAiOauth"].(map[string]any); ok {
			content = v
		}
		for _, pair := range [][2]string{{"access_token", "accessToken"}, {"refresh_token", "refreshToken"}} {
			out[pair[0]] = firstString(content, pair[0], pair[1])
		}
		if out["refresh_token"] == "" {
			return nil, errors.New("Claude refresh token is required")
		}
		if v, ok := content["expiresAt"].(float64); ok {
			if v < 0 || v > 253402300799000 {
				return nil, errors.New("invalid credential expiration")
			}
			out["expired"] = time.UnixMilli(int64(v)).UTC().Format(time.RFC3339)
		} else if v := firstString(content, "expired"); v != "" {
			out["expired"] = v
		}
	case "codex":
		if v, ok := content["tokens"].(map[string]any); ok {
			content = v
		}
		for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id"} {
			if v := firstString(content, k); v != "" {
				out[k] = v
			}
		}
		if out["access_token"] == nil || out["refresh_token"] == nil {
			return nil, errors.New("Codex OAuth access and refresh tokens are required")
		}
		out["websockets"] = true
	default:
		return nil, errors.New("choose claude or codex")
	}
	return out, nil
}
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
func credentialName(provider string, m map[string]any) string {
	identity := firstString(m, "account_id", "refresh_token")
	h := sha256.Sum256([]byte(provider + ":" + identity))
	return "subscription-" + provider + "-" + hex.EncodeToString(h[:12]) + ".json"
}
func (s *service) importCredential(provider string, raw json.RawMessage, apply bool) (any, error) {
	m, err := normalize(provider, raw)
	if err != nil {
		return nil, err
	}
	name := credentialName(provider, m)
	s.importMu.Lock()
	defer s.importMu.Unlock()
	b, err := s.host("host.auth.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var list struct {
		Files []struct {
			Name string `json:"name"`
		}
	}
	if json.Unmarshal(b, &list) != nil {
		return nil, errors.New("invalid host credential list")
	}
	for _, f := range list.Files {
		if f.Name == name {
			return nil, errors.New("credential already imported; existing tokens were not replaced")
		}
	}
	if apply {
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		if _, err = s.host("host.auth.save", map[string]any{"name": name, "json": json.RawMessage(b)}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"provider": provider, "name": name, "imported": apply, "has_access_token": m["access_token"] != "" && m["access_token"] != nil, "has_refresh_token": true}, nil
}
