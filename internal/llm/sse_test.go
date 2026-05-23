package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSSEServer returns an httptest.Server that replays the given SSE body
// for any POST. The body should already be in SSE wire format ("data: ...\n\n").
func fakeSSEServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
}

func TestOpenAIDriver_CompleteStream_AccumulatesAndCountsUsage(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":", "}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"world"}}]}`,
		``,
		`data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	srv := fakeSSEServer(t, body)
	defer srv.Close()

	d := newOpenAI("test", Opts{APIKey: "k", ModelFlash: "m-flash", ModelPro: "m-pro"}, srv.URL)
	var got string
	res, err := d.CompleteStream(context.Background(), ModelFlashKey, "sys", "user", StreamHandler{
		OnContent: func(chunk string) { got += chunk },
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Text != "Hello, world" || got != "Hello, world" {
		t.Errorf("text=%q chunks=%q", res.Text, got)
	}
	if res.TokensIn != 12 || res.TokensOut != 3 {
		t.Errorf("tokens=%d/%d want 12/3", res.TokensIn, res.TokensOut)
	}
	if res.Model != "m-flash" {
		t.Errorf("model=%q", res.Model)
	}
}

func TestOpenAIDriver_CompleteStream_IgnoresMalformedFrames(t *testing.T) {
	body := strings.Join([]string{
		`data: not json at all`,
		``,
		`data: {"choices":[{"delta":{"content":"A"}}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	srv := fakeSSEServer(t, body)
	defer srv.Close()

	d := newOpenAI("test", Opts{APIKey: "k", ModelFlash: "m", ModelPro: "p"}, srv.URL)
	res, err := d.CompleteStream(context.Background(), ModelFlashKey, "", "u", StreamHandler{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Text != "A" {
		t.Errorf("text=%q want A", res.Text)
	}
}

func TestAnthropicDriver_CompleteStream_AccumulatesAndUsage(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":50,"output_tokens":0}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
	srv := fakeSSEServer(t, body)
	defer srv.Close()

	d := newAnthropic(Opts{APIKey: "k", ModelFlash: "claude-flash", ModelPro: "claude-pro"}, srv.URL)
	var got string
	res, err := d.CompleteStream(context.Background(), ModelProKey, "sys", "user", StreamHandler{
		OnContent: func(chunk string) { got += chunk },
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Text != "Hello world" || got != "Hello world" {
		t.Errorf("text=%q chunks=%q", res.Text, got)
	}
	if res.TokensIn != 50 {
		t.Errorf("input tokens=%d want 50", res.TokensIn)
	}
	if res.TokensOut != 2 {
		t.Errorf("output tokens=%d want 2", res.TokensOut)
	}
	if res.Model != "claude-pro" {
		t.Errorf("model=%q", res.Model)
	}
}

func TestAnthropicDriver_CompleteStream_SkipsThinkingDeltas(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning..."}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
	srv := fakeSSEServer(t, body)
	defer srv.Close()

	d := newAnthropic(Opts{APIKey: "k", ModelFlash: "f", ModelPro: "p"}, srv.URL)
	res, err := d.CompleteStream(context.Background(), ModelFlashKey, "", "u", StreamHandler{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Text != "answer" {
		t.Errorf("text=%q want 'answer' (thinking should be skipped)", res.Text)
	}
}

func TestAnthropicDriver_ThinkingBudgetSetsHeader(t *testing.T) {
	if got := anthropicThinkingBudget("off"); got != 0 {
		t.Errorf("off=%d want 0", got)
	}
	if got := anthropicThinkingBudget("medium"); got != 12000 {
		t.Errorf("medium=%d want 12000", got)
	}
	if got := anthropicThinkingBudget("garbage"); got != 0 {
		t.Errorf("garbage=%d want 0", got)
	}

	d := newAnthropic(Opts{APIKey: "k", ModelFlash: "f", ModelPro: "p", ReasoningPro: "high"}, "http://example/")
	req := d.buildReq(ModelProKey, "s", "u", false)
	if req.Thinking == nil {
		t.Fatalf("expected thinking enabled on pro tier")
	}
	if req.Thinking.BudgetTokens != 32000 {
		t.Errorf("budget=%d want 32000", req.Thinking.BudgetTokens)
	}
	if req.MaxTokens <= 32000 {
		t.Errorf("max_tokens=%d should exceed budget", req.MaxTokens)
	}

	// flash tier has no reasoning set => no thinking
	req2 := d.buildReq(ModelFlashKey, "s", "u", false)
	if req2.Thinking != nil {
		t.Errorf("flash should not have thinking enabled: %+v", req2.Thinking)
	}
}

func TestOpenAIEffortMapping(t *testing.T) {
	if got := openaiEffort("off"); got != "" {
		t.Errorf("off=%q want empty", got)
	}
	if got := openaiEffort("medium"); got != "medium" {
		t.Errorf("medium=%q", got)
	}
	if got := openaiEffort("garbage"); got != "" {
		t.Errorf("garbage=%q want empty", got)
	}
}
