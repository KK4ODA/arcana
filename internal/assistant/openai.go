package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is what Arcana asks a model to do.
type ChatRequest struct {
	Messages    []Message
	MaxTokens   int
	Temperature float64
}

// ChatResult is a finished generation.
type ChatResult struct {
	Text string
	// Truncated is true when the model stopped at MaxTokens.
	Truncated        bool
	PromptTokens     int
	CompletionTokens int
}

// Backend generates text. onToken receives visible text as it arrives
// (reasoning is removed); it may be nil.
type Backend interface {
	Chat(ctx context.Context, req ChatRequest, onToken func(string)) (ChatResult, error)
}

// Client talks to an OpenAI-compatible chat completions endpoint: llamafile
// and llama.cpp's server, Ollama, LM Studio and similar. BaseURL ends in
// "/v1" (it is added when missing).
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	// Extra fields are merged into every request (for example llama.cpp's
	// chat_template_kwargs). Only send fields the server is known to accept.
	Extra map[string]any
	HTTP  *http.Client
}

// NormalizeBaseURL validates an OpenAI-compatible base address and makes
// sure it ends in /v1.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("enter an http:// or https:// address, such as http://127.0.0.1:11434/v1 for Ollama")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the server address cannot contain ? or #")
	}
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(p, "/chat/completions")
	if !strings.HasSuffix(p, "/v1") {
		p += "/v1"
	}
	u.Path = p
	return u.String(), nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// No overall timeout: answers stream for as long as the model writes.
	// The request context bounds it instead.
	return &http.Client{Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Minute}}
}

func (c *Client) endpoint(p string) string {
	return strings.TrimRight(c.BaseURL, "/") + p
}

func (c *Client) authorize(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
}

// Models lists the models the server offers (used to test a connection and
// to fill in the model name).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/models"), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the model server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, serverError(resp)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("the server did not answer like an OpenAI-compatible API: %w", err)
	}
	var out []string
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

func serverError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var e struct {
		Error any `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Error != nil {
		switch v := e.Error.(type) {
		case string:
			msg = v
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				msg = m
			}
		}
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("the model server refused the request (%d): check the API key. %s", resp.StatusCode, msg)
	}
	return fmt.Errorf("the model server answered %d: %s", resp.StatusCode, msg)
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error any `json:"error"`
}

// Chat streams a chat completion.
func (c *Client) Chat(ctx context.Context, cr ChatRequest, onToken func(string)) (ChatResult, error) {
	body := map[string]any{
		"model":       c.Model,
		"messages":    cr.Messages,
		"stream":      true,
		"temperature": cr.Temperature,
	}
	if cr.MaxTokens > 0 {
		body["max_tokens"] = cr.MaxTokens
	}
	for k, v := range c.Extra {
		body[k] = v
	}
	data, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/chat/completions"), bytes.NewReader(data))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ChatResult{}, ctx.Err()
		}
		return ChatResult{}, fmt.Errorf("cannot reach the model server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ChatResult{}, serverError(resp)
	}
	var res ChatResult
	var text strings.Builder
	filter := &thinkFilter{}
	emit := func(s string) {
		if v := filter.Write(s); v != "" {
			text.WriteString(v)
			if onToken != nil {
				onToken(v)
			}
		}
	}
	// A server that ignores "stream" answers with one JSON object.
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var whole streamChunk
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&whole); err != nil {
			return res, fmt.Errorf("unexpected answer from the model server: %w", err)
		}
		if len(whole.Choices) > 0 {
			emit(whole.Choices[0].Message.Content)
			if fr := whole.Choices[0].FinishReason; fr != nil && *fr == "length" {
				res.Truncated = true
			}
		}
		if whole.Usage != nil {
			res.PromptTokens, res.CompletionTokens = whole.Usage.PromptTokens, whole.Usage.CompletionTokens
		}
		emit(filter.Flush())
		res.Text = text.String()
		return res, nil
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			continue
		}
		if ch.Error != nil {
			return res, fmt.Errorf("the model server reported an error: %v", ch.Error)
		}
		for _, choice := range ch.Choices {
			emit(choice.Delta.Content)
			if choice.FinishReason != nil && *choice.FinishReason == "length" {
				res.Truncated = true
			}
		}
		if ch.Usage != nil {
			res.PromptTokens, res.CompletionTokens = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, fmt.Errorf("the answer was interrupted: %w", err)
	}
	emit(filter.Flush())
	res.Text = text.String()
	return res, nil
}

// thinkFilter removes <think>…</think> reasoning that some models write
// before their answer. Tags may be split across stream chunks.
type thinkFilter struct {
	pending string
	inThink bool
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

func (f *thinkFilter) Write(s string) string {
	f.pending += s
	var out strings.Builder
	for {
		if f.inThink {
			i := strings.Index(f.pending, thinkClose)
			if i < 0 {
				// Keep a possible partial closing tag.
				if len(f.pending) > len(thinkClose) {
					f.pending = f.pending[len(f.pending)-len(thinkClose):]
				}
				return out.String()
			}
			f.pending = strings.TrimLeft(f.pending[i+len(thinkClose):], "\r\n ")
			f.inThink = false
			continue
		}
		i := strings.Index(f.pending, thinkOpen)
		if i >= 0 {
			out.WriteString(f.pending[:i])
			f.pending = f.pending[i+len(thinkOpen):]
			f.inThink = true
			continue
		}
		// Hold back a suffix that could be the start of "<think>".
		keep := 0
		for n := len(thinkOpen) - 1; n > 0; n-- {
			if strings.HasSuffix(f.pending, thinkOpen[:n]) {
				keep = n
				break
			}
		}
		out.WriteString(f.pending[:len(f.pending)-keep])
		f.pending = f.pending[len(f.pending)-keep:]
		return out.String()
	}
}

// Flush returns held-back text at the end of a stream. Unfinished
// reasoning is dropped.
func (f *thinkFilter) Flush() string {
	if f.inThink {
		f.pending = ""
		return ""
	}
	s := f.pending
	f.pending = ""
	return s
}
