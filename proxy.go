package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type APIStyle int

const (
	APIStyleOpenAI APIStyle = iota
	APIStyleAnthropic
)

func (a *App) proxyV1(w http.ResponseWriter, r *http.Request, style APIStyle) {
	r.Body = http.MaxBytesReader(w, r.Body, a.config.MaxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()
	orig := r.Context()
	for attempts := 0; attempts < len(a.config.UpstreamAPIKeys); attempts++ {
		idx, key, ok := a.keys.Current()
		if !ok {
			break
		}
		resp, reqErr := a.doUpstream(orig, r, body, a.config.UpstreamBaseURL, key, style)
		if reqErr != nil {
			http.Error(w, reqErr.Error(), http.StatusBadGateway)
			return
		}
		if isKeyDead(resp) {
			_ = resp.Body.Close()
			a.keys.MarkExhausted(idx)
			if a.keys.ShouldNotifySwitch(idx) {
				a.sender.NotifySwitch(idx, a.keys.Status())
			}
			continue
		}
		// The key served the request, so it is healthy: mark it available so a
		// recovered (previously exhausted) key un-sticks and the notification
		// flags re-arm for the next depletion round.
		a.keys.MarkAvailable(idx)
		copyResponse(w, resp)
		return
	}
	if a.proxyTiers(orig, w, r, body, style) {
		return
	}
	// No eligible key remains. Fail fast locally instead of hammering upstream,
	// and hint well-behaved clients at the next probe window via Retry-After.
	if a.keys.ShouldNotifyAllExhausted() {
		a.sender.NotifyAllExhausted(a.keys.Status())
	}
	if secs, ok := a.keys.RetryAfterSeconds(); ok {
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	writeAPIError(w, style, http.StatusTooManyRequests, "rate_limit_exceeded", "all upstream keys exhausted")
}

// proxyTiers tries the configured tiers in order once tier 0 is exhausted and
// reports whether one answered. A request without a top-level string "model"
// stays on tier 0.
func (a *App) proxyTiers(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, style APIStyle) bool {
	var fields map[string]json.RawMessage
	var model string
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["model"], &model) != nil || model == "" {
		return false
	}
	for _, t := range a.config.Tiers {
		id, ok := tierModelID(t, model, style)
		if !ok {
			continue
		}
		raw, _ := json.Marshal(id)
		fields["model"] = raw
		tierBody, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		resp, err := a.doUpstream(ctx, r, tierBody, t.BaseURL, t.APIKey, style)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return true
		}
		if isQuota429(resp) {
			_ = resp.Body.Close()
			continue
		}
		copyResponse(w, resp)
		return true
	}
	return false
}

// tierModelID returns the upstream model ID for model when the tier speaks style and maps it.
func tierModelID(t TierConfig, model string, style APIStyle) (string, bool) {
	speaks := false
	for _, s := range t.Styles {
		speaks = speaks || s == style
	}
	if !speaks {
		return "", false
	}
	for _, m := range t.Models {
		if m.Name == model {
			if m.ID != "" {
				return m.ID, true
			}
			return m.Name, true
		}
	}
	return "", false
}

func (a *App) doUpstream(ctx context.Context, r *http.Request, body []byte, baseURL, key string, apiStyle APIStyle) (*http.Response, error) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/v1")
	if path == "" {
		path = "/"
	}
	u := strings.TrimRight(baseURL, "/") + path
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, r.Header)
	// Let the Transport negotiate gzip itself: it only decompresses transparently
	// when it set Accept-Encoding, and isKeyDead needs to read the plain body.
	req.Header.Del("Accept-Encoding")
	if apiStyle == APIStyleAnthropic {
		req.Header.Set("x-api-key", key)
		if strings.TrimSpace(req.Header.Get("anthropic-version")) == "" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
		if strings.TrimSpace(req.Header.Get("User-Agent")) == "" {
			req.Header.Set("User-Agent", "anthropic-sdk-go/1.0.0")
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
		if strings.TrimSpace(req.Header.Get("User-Agent")) == "" {
			req.Header.Set("User-Agent", "OpenAI/Python 1.0.0")
		}
	}
	stripHopByHopHeaders(req.Header)
	return a.client.Do(req)
}

func apiStyleForRequest(r *http.Request) APIStyle {
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	if strings.HasPrefix(path, "/messages") || strings.HasPrefix(path, "/complete") {
		return APIStyleAnthropic
	}
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "anthropic-") {
			return APIStyleAnthropic
		}
	}
	return APIStyleOpenAI
}

func copyHeaders(dst, src http.Header) {
	for k, v := range src {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "x-api-key") {
			continue
		}
		for _, s := range v {
			dst.Add(k, s)
		}
	}
}

func stripHopByHopHeaders(h http.Header) {
	hop := map[string]struct{}{"Connection": {}, "Proxy-Authorization": {}, "Proxy-Authenticate": {}, "Keep-Alive": {}, "Te": {}, "Trailer": {}, "Transfer-Encoding": {}, "Upgrade": {}}
	for _, v := range h.Values("Connection") {
		for _, part := range strings.Split(v, ",") {
			if name := strings.TrimSpace(part); name != "" {
				hop[http.CanonicalHeaderKey(name)] = struct{}{}
			}
		}
	}
	for k := range hop {
		h.Del(k)
	}
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	stripHopByHopHeaders(resp.Header)
	for k, v := range resp.Header {
		for _, s := range v {
			w.Header().Add(k, s)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	isStream := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")

	if canFlush && isStream {
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	} else {
		_, _ = io.Copy(w, resp.Body)
	}
}

// isKeyDead reports whether the upstream response means this key cannot serve
// requests and the next one should be tried: a quota 429, or the 403 OpenCode Go
// returns when the key's subscription is gone. Like isQuota429, it restores resp.Body.
func isKeyDead(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden {
		return isQuota429(resp)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return strings.Contains(strings.ToLower(string(body)), "subscription is required")
}

func isQuota429(resp *http.Response) bool {
	if resp.StatusCode != 429 {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		if e, ok := m["error"].(map[string]any); ok {
			if code, _ := e["code"].(string); code == "insufficient_quota" || code == "usage_not_included" {
				return true
			}
			typ, _ := e["type"].(string)
			msg, _ := e["message"].(string)
			lowType := strings.ToLower(typ)
			lowMsg := strings.ToLower(msg)
			if strings.Contains(lowType, "usage") || strings.Contains(lowType, "quota") || strings.Contains(lowType, "freeusagelimit") {
				return true
			}
			if strings.Contains(lowMsg, "quota") || strings.Contains(lowMsg, "exhausted") || strings.Contains(lowMsg, "usage limit") || strings.Contains(lowMsg, "credit balance") || strings.Contains(lowMsg, "billing limit") {
				return true
			}
		}
	}
	return strings.Contains(strings.ToLower(resp.Header.Get("X-RateLimit-Reason")), "quota")
}

func writeAPIError(w http.ResponseWriter, style APIStyle, status int, code, message string) {
	if style == APIStyleAnthropic {
		writeAnthropicError(w, status, code, message)
		return
	}
	writeOpenAIError(w, status, code, message)
}

func writeOpenAIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message, "type": "invalid_request_error", "param": nil, "code": code}})
}

func writeAnthropicError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": anthropicErrorType(status, code), "message": message}})
}

func anthropicErrorType(status int, code string) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication_error"
	case status == http.StatusTooManyRequests || code == "rate_limit_exceeded":
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	default:
		return "invalid_request_error"
	}
}
