package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"time"
)

type pacingResult struct {
	Planned   int       `json:"planned_accounts"`
	Changed   int       `json:"changed_accounts"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}
type planItem struct {
	AuthIndex string `json:"auth_index"`
	Provider  string `json:"provider"`
	Fresh     bool   `json:"evidence_fresh"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Target    struct {
		Priority *int `json:"priority"`
		Weight   *int `json:"weight"`
	} `json:"target"`
}

var workerMu sync.Mutex

func (s *service) stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (s *service) configure(c config) error {
	u, err := url.Parse(c.ManagementURL)
	if err != nil || u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback() || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("management_url must be an HTTP loopback origin")
	}
	if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(c.ManagementKeyEnv) || c.IntervalSeconds < 60 || c.IntervalSeconds > 3600 {
		return errors.New("invalid pacing settings")
	}
	if c.PacingEnabled && os.Getenv(c.ManagementKeyEnv) == "" {
		return errors.New("management key environment variable is missing")
	}
	s.stop()
	s.mu.Lock()
	s.config = c
	s.mu.Unlock()
	if c.PacingEnabled {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		s.mu.Lock()
		s.cancel = cancel
		s.done = done
		s.mu.Unlock()
		go func() {
			defer close(done)
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					s.pace(ctx, c)
					timer.Reset(time.Duration(c.IntervalSeconds) * time.Second)
				}
			}
		}()
	}
	return nil
}
func localRequest(ctx context.Context, c config, method, path string, data any, out any) error {
	var body io.Reader
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.ManagementURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(c.ManagementKeyEnv))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("local management request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("local management returned HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
func (s *service) pace(ctx context.Context, c config) pacingResult {
	workerMu.Lock()
	defer workerMu.Unlock()
	result := pacingResult{CheckedAt: time.Now().UTC()}
	err := applyPacing(ctx, c, &result)
	if err != nil {
		result.Error = err.Error()
	}
	s.mu.Lock()
	s.last = result
	s.mu.Unlock()
	return result
}
func applyPacing(ctx context.Context, c config, result *pacingResult) error {
	var cfg struct {
		Routing struct {
			Strategy string `json:"strategy"`
		}
		Plugins struct {
			Configs map[string]struct {
				AutoApply bool `json:"auto_apply"`
			} `json:"configs"`
		}
	}
	if err := localRequest(ctx, c, "GET", "/v8/management/config", nil, &cfg); err != nil {
		return err
	}
	if cfg.Routing.Strategy != "weighted-round-robin" {
		return errors.New("weighted-round-robin routing is required")
	}
	if cfg.Plugins.Configs["quota-pacer"].AutoApply {
		return errors.New("disable Quota Pacer auto_apply before enabling this bridge")
	}
	var p struct {
		Attempted int `json:"attempted"`
		Snapshot  struct {
			Items []planItem `json:"items"`
		}
	}
	if err := localRequest(ctx, c, "POST", "/v0/management/plugins/quota-pacer/run?mode=dry-run&provider=claude&provider=codex", map[string]any{}, &p); err != nil {
		return err
	}
	if p.Attempted != 0 {
		return errors.New("Quota Pacer performed an unexpected write")
	}
	desired := map[string]map[string]any{}
	for _, item := range p.Snapshot.Items {
		if !item.Fresh || item.Status != "active" || (item.Provider != "claude" && item.Provider != "codex") {
			continue
		}
		if item.Target.Priority == nil {
			return errors.New("invalid plugin priority")
		}
		fields := map[string]any{"priority": *item.Target.Priority}
		if item.Reason == "fresh remaining depleted" {
			fields["weight"] = 0
		} else if item.Target.Weight != nil {
			if *item.Target.Weight < 0 || *item.Target.Weight > 1000000 {
				return errors.New("invalid plugin weight")
			}
			fields["weight"] = *item.Target.Weight
		}
		desired[item.AuthIndex] = fields
	}
	result.Planned = len(desired)
	var accounts struct {
		Files []struct {
			Name      string `json:"name"`
			AuthIndex string `json:"auth_index"`
			Disabled  bool   `json:"disabled"`
			Priority  int    `json:"priority"`
			Weight    *int   `json:"weight"`
		}
	}
	if err := localRequest(ctx, c, "GET", "/v8/management/credentials", nil, &accounts); err != nil {
		return err
	}
	for _, a := range accounts.Files {
		if a.Disabled {
			continue
		}
		fields := map[string]any{"name": a.Name}
		for k, v := range desired[a.AuthIndex] {
			old := a.Priority
			if k == "weight" {
				old = 1
				if a.Weight != nil {
					old = *a.Weight
				}
			}
			if v != old {
				fields[k] = v
			}
		}
		if len(fields) > 1 {
			if err := localRequest(ctx, c, "PATCH", "/v8/management/credentials/fields", fields, nil); err != nil {
				return err
			}
			result.Changed++
		}
	}
	return nil
}
