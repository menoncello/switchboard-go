package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const tier0Name = "opencode-go"

// tier0CooldownKey keys tier 0 in tierCooldowns; an extra tier cannot have an empty name.
const tier0CooldownKey = ""

// maxLoggedModel bounds the model in the request log so a client cannot inflate a log line.
const maxLoggedModel = 128

type APIStyle int

const (
	APIStyleOpenAI APIStyle = iota
	APIStyleAnthropic
)

// statusRecorder keeps the first status the client was sent and still flushes.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// proxyV1 serves one proxied request and logs one JSON line when the response
// (including a stream) is done. The line never carries a key value.
func (a *App) proxyV1(w http.ResponseWriter, r *http.Request, style APIStyle) {
	start := time.Now()
	// Limit with the original writer so net/http still sees an oversized body and closes the connection.
	r.Body = http.MaxBytesReader(w, r.Body, a.config.MaxRequestBodyBytes)
	rec := &statusRecorder{ResponseWriter: w}
	model, tier, keyIdx := a.serveProxy(rec, r, style)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	apiStyle := "openai"
	if style == APIStyleAnthropic {
		apiStyle = "anthropic"
	}
	if len(model) > maxLoggedModel {
		cut := maxLoggedModel
		for !utf8.RuneStart(model[cut]) {
			cut--
		}
		model = model[:cut]
	}
	attrs := []any{"model", model, "api_style", apiStyle, "tier", tier}
	if keyIdx >= 0 {
		attrs = append(attrs, "key_index", keyIdx, "key_name", a.config.keyName(keyIdx))
	}
	attrs = append(attrs, "status", rec.status, "latency_ms", time.Since(start).Milliseconds())
	a.logger.Info("request", attrs...)
}

// serveProxy does the work of proxyV1 and returns what the log line needs: the
// top-level model, the tier that answered ("" when none did) and the tier 0 key index (-1 when tier 0 did not answer).
func (a *App) serveProxy(w http.ResponseWriter, r *http.Request, style APIStyle) (model, tier string, keyIdx int) {
	keyIdx = -1
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
	fields, model := topLevelModel(body)
	orig := r.Context()
	var last heldResponse
	defer last.release()
	if a.cooldowns.remaining(tier0CooldownKey) == 0 {
		for attempts := 0; attempts < len(a.config.UpstreamAPIKeys); attempts++ {
			idx, key, ok := a.keys.Current()
			if !ok {
				break
			}
			resp, reqErr := a.doUpstream(orig, r, body, a.config.UpstreamBaseURL, key, style)
			if reqErr != nil {
				if orig.Err() != nil {
					w.WriteHeader(statusClientClosed)
					return model, "", -1
				}
				// Connection error or timeout: no cooldown, no key rotation, on to the extra tiers.
				last.err = reqErr
				break
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
			if isTierFailure(resp, model != "") {
				// The whole tier is unhealthy, not one key: rotating keys would not help.
				a.cooldowns.set(tier0CooldownKey, a.config.RetryExhaustedAfter)
				last.hold(resp, tier0Name, idx)
				break
			}
			copyResponse(w, resp)
			return model, tier0Name, idx
		}
	}
	if name, ok := a.proxyTiers(orig, w, r, fields, model, style, &last); ok {
		return model, name, -1
	}
	if orig.Err() != nil {
		w.WriteHeader(statusClientClosed)
		return model, "", -1
	}
	// No tier served it; alert once when every tier 0 key is exhausted.
	if a.keys.ShouldNotifyAllExhausted() {
		a.sender.NotifyAllExhausted(a.keys.Status())
	}
	if last.resp != nil {
		copyResponse(w, last.resp)
		return model, last.tier, last.key
	}
	if last.err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway) // fixed text: the error would leak the tier URL
		return model, "", -1
	}
	// No eligible tier remains. Fail fast locally instead of hammering upstream,
	// and hint well-behaved clients at the first window via Retry-After.
	if secs, ok := a.localRetryAfter(model, style); ok {
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	writeAPIError(w, style, http.StatusTooManyRequests, "rate_limit_exceeded", "all upstream keys exhausted")
	return model, "", -1
}

// heldResponse is the last failure response seen, kept so the client can get it
// when no tier is left. Its body stays readable until released or served.
type heldResponse struct {
	resp *http.Response
	tier string
	key  int
	err  error // last connection error or timeout, served as 502 when no response is held
}

// statusClientClosed is logged when the client cancels the request (nginx's 499).
const statusClientClosed = 499

func (h *heldResponse) hold(resp *http.Response, tier string, key int) {
	h.release()
	*h = heldResponse{resp: resp, tier: tier, key: key, err: h.err}
}

func (h *heldResponse) release() {
	if h.resp != nil {
		_ = h.resp.Body.Close()
	}
}

// localRetryAfter is the seconds until the first eligible tier is tried again:
// tier 0 (its tier cooldown or, without one, its exhausted keys) and the extra
// tiers that map model and style and are in cooldown. False when tier 0 has no
// known time (nothing to promise).
func (a *App) localRetryAfter(model string, style APIStyle) (int, bool) {
	soonest := a.cooldowns.remaining(tier0CooldownKey)
	if soonest <= 0 {
		secs, ok := a.keys.RetryAfterSeconds()
		if !ok {
			return 0, false
		}
		soonest = time.Duration(secs) * time.Second
	}
	if model != "" {
		for _, t := range a.config.Tiers {
			if _, ok := tierModelID(t, model, style); !ok {
				continue
			}
			if d := a.cooldowns.remaining(t.Name); d > 0 && d < soonest {
				soonest = d
			}
		}
	}
	return max(1, int(math.Ceil(soonest.Seconds()))), true
}

// isTierFailure reports whether resp means the whole tier is unhealthy and the
// next one should be tried: 401, 402, 429, any 5xx, and a 404 that is not about a
// missing model (only when the request names a model). Like isKeyDead, it restores resp.Body.
func isTierFailure(resp *http.Response, hasModel bool) bool {
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 402 || resp.StatusCode == 429 || resp.StatusCode >= 500:
		return true
	case resp.StatusCode == 404 && hasModel:
		low := strings.ToLower(string(peekBody(resp)))
		if !strings.Contains(low, "model") {
			return true
		}
		for _, p := range []string{"not found", "does not exist", "unknown", "invalid", "no such"} {
			if strings.Contains(low, p) {
				return false
			}
		}
		return true
	}
	return false
}

// proxyTiers tries the configured tiers in order after tier 0 failed or was
// skipped and returns the name of the tier that answered and whether one did (the
// name is empty when none did). A tier that fails with a response is put in
// cooldown and its response kept in last; a connection error or timeout only
// records the error in last and moves on, with no cooldown. A request without a top-level string
// "model" stays on tier 0.
func (a *App) proxyTiers(ctx context.Context, w http.ResponseWriter, r *http.Request, fields map[string]json.RawMessage, model string, style APIStyle, last *heldResponse) (string, bool) {
	if model == "" {
		return "", false
	}
	for _, t := range a.config.Tiers {
		id, ok := tierModelID(t, model, style)
		if !ok || a.cooldowns.remaining(t.Name) > 0 {
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
			last.err = errors.New("tier upstream unreachable")
			if ctx.Err() != nil {
				return "", false
			}
			continue
		}
		if isTierFailure(resp, true) {
			cooldown := a.config.RetryExhaustedAfter
			if t.Cooldown != nil {
				cooldown = *t.Cooldown
			}
			a.cooldowns.set(t.Name, cooldown)
			last.hold(resp, t.Name, -1)
			continue
		}
		copyResponse(w, resp)
		return t.Name, true
	}
	return "", false
}

// topLevelModel parses body once and returns its fields and the top-level string
// "model" ("" when the body is not a JSON object or has no string model).
func topLevelModel(body []byte) (map[string]json.RawMessage, string) {
	var fields map[string]json.RawMessage
	var model string
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["model"], &model) != nil {
		return nil, ""
	}
	return fields, model
}

// tierModelID returns the upstream model ID for model when the tier speaks style and maps it.
func tierModelID(t TierConfig, model string, style APIStyle) (string, bool) {
	if !slices.Contains(t.Styles, style) {
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

// peekBody returns the first 4096 bytes of resp.Body and leaves the whole body
// readable: the peeked bytes are put back in front of the rest.
func peekBody(resp *http.Response) []byte {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
	return body
}

// isKeyDead reports whether the upstream response means this key cannot serve
// requests and the next one should be tried: a quota 429, or the 403 OpenCode Go
// returns when the key's subscription is gone. Like isQuota429, it restores resp.Body.
func isKeyDead(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden {
		return isQuota429(resp)
	}
	return strings.Contains(strings.ToLower(string(peekBody(resp))), "subscription is required")
}

func isQuota429(resp *http.Response) bool {
	if resp.StatusCode != 429 {
		return false
	}
	body := peekBody(resp)
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
