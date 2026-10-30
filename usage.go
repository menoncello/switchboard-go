package main

import (
	"bytes"
	"encoding/json"
)

// maxUsageBody bounds what is buffered to read usage from a JSON response and
// the longest SSE line the scanner keeps; above it the usage is ignored.
const maxUsageBody = 1 << 20

// usageTokens is the token count a response reported in its `usage` field.
type usageTokens struct {
	in, out, cache int
	found          bool
	partial        bool // the stream broke before its end; the counts are what had arrived
}

type usageFields struct {
	PromptTokens     int `json:"prompt_tokens"`
	InputTokens      int `json:"input_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	OutputTokens     int `json:"output_tokens"`

	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	PromptDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// parseUsage reads `usage` from the top of raw (OpenAI, Anthropic JSON, stream chunk) or from
// `message.usage` (Anthropic message_start). Anything else, including malformed JSON, is not found.
func parseUsage(raw []byte) usageTokens {
	var v struct {
		Usage   *usageFields `json:"usage"`
		Message *struct {
			Usage *usageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return usageTokens{}
	}
	u := v.Usage
	if u == nil && v.Message != nil {
		u = v.Message.Usage
	}
	if u == nil {
		return usageTokens{}
	}
	t := usageTokens{in: max(u.PromptTokens, u.InputTokens), out: max(u.CompletionTokens, u.OutputTokens),
		cache: u.CacheCreation + u.CacheRead + u.PromptDetails.CachedTokens}
	t.found = t.in > 0 || t.out > 0
	return t
}

// sseUsageScanner is an io.Writer fed with an SSE stream. It keeps the last non-zero in, out and cache
// seen in `data:` lines, so Anthropic's input_tokens (message_start) and cumulative output_tokens
// (message_delta) end up together. It never fails a write.
type sseUsageScanner struct {
	line []byte
	usageTokens
}

func (s *sseUsageScanner) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if len(s.line)+len(p) > maxUsageBody {
				s.line = s.line[:0]
			} else {
				s.line = append(s.line, p...)
			}
			break
		}
		if len(s.line)+i <= maxUsageBody {
			s.line = append(s.line, p[:i]...)
			s.scan(s.line)
		}
		s.line = s.line[:0]
		p = p[i+1:]
	}
	return n, nil
}

// result scans a last line left without a trailing newline and returns the tokens.
func (s *sseUsageScanner) result() usageTokens {
	s.scan(s.line)
	s.line = s.line[:0]
	return s.usageTokens
}

func (s *sseUsageScanner) scan(line []byte) {
	if !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	if !ok {
		return
	}
	t := parseUsage(data)
	if t.in != 0 {
		s.in = t.in
	}
	if t.out != 0 {
		s.out = t.out
	}
	if t.cache != 0 {
		s.cache = t.cache
	}
	s.found = s.in > 0 || s.out > 0
}

// cappedBuffer collects up to maxUsageBody bytes, drops the rest and never fails a write.
type cappedBuffer struct {
	b        bytes.Buffer
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.overflow || c.b.Len()+len(p) > maxUsageBody {
		c.overflow = true
		return len(p), nil
	}
	return c.b.Write(p)
}
