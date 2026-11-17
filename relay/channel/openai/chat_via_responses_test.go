package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newResponsesChatTestContext(t *testing.T, body string, isStream bool) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "responses-test")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:           isStream,
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		DisablePing:        true,
	}
	return c, recorder, resp, info
}

func TestOaiResponsesToChatStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `"role":"assistant"`)
	require.Contains(t, got, `"content":"hello"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
	require.Contains(t, got, `"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`)
	require.Contains(t, got, `data: [DONE]`)
	requireOrderedSubstrings(t, got,
		`"role":"assistant"`,
		`"content":"hello"`,
		`"name":"lookup"`,
		`"arguments":"{\"q\":\"x\"}"`,
		`"finish_reason":"tool_calls"`,
		`"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`,
		`data: [DONE]`,
	)
}

func TestOaiResponsesToChatStreamHandlerConvertsClaudeSSETerminalsAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	info.RelayFormat = types.RelayFormatClaude

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, 1, strings.Count(got, "event: message_start\n"))
	assert.Equal(t, 1, strings.Count(got, "event: content_block_stop\n"))
	assert.Equal(t, 1, strings.Count(got, "event: message_delta\n"))
	assert.Equal(t, 1, strings.Count(got, "event: message_stop\n"))

	messageDeltaFrame := ""
	for frame := range strings.SplitSeq(got, "\n\n") {
		if strings.HasPrefix(frame, "event: message_delta\n") {
			messageDeltaFrame = frame
			break
		}
	}
	require.NotEmpty(t, messageDeltaFrame)
	assert.Contains(t, messageDeltaFrame, `"type":"message_delta"`)
	assert.Contains(t, messageDeltaFrame, `"stop_reason":"end_turn"`)
	assert.Contains(t, messageDeltaFrame, `"input_tokens":2`)
	assert.Contains(t, messageDeltaFrame, `"output_tokens":3`)
	requireOrderedSubstrings(t, got,
		"event: message_start\n",
		"event: content_block_stop\n",
		"event: message_delta\n",
		"event: message_stop\n",
	)
}

func TestOaiResponsesToChatBufferedStreamHandlerReturnsJSONFromSSE(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"buffered text"}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.done","response":{"model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

	usage, err := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.TotalTokens)

	got := recorder.Body.String()
	require.NotContains(t, got, `data:`)
	require.Contains(t, got, `"object":"chat.completion"`)
	require.Contains(t, got, `"content":"buffered text"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
}

func TestOaiResponsesToChatBufferedStreamHandlerPreservesInterleavedClaudeContent(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"**Planning file inspection**"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","delta":"I’ll inspect the starter repository."}`,
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"reasoning","id":"rs_2","summary":[]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","output_index":2,"item_id":"rs_2","delta":"**Clarifying environment task requirements**"}`,
		`data: {"type":"response.output_item.added","output_index":3,"item":{"type":"message","id":"msg_2","role":"assistant","content":[]}}`,
		`data: {"type":"response.output_text.delta","output_index":3,"item_id":"msg_2","delta":"What would you like me to build?"}`,
		`data: {"type":"response.done","response":{"id":"resp_1","model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	info.RelayFormat = types.RelayFormatClaude

	usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.TotalTokens)

	var claudeResponse dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &claudeResponse))
	require.Len(t, claudeResponse.Content, 4)
	assert.Equal(t, []string{"thinking", "text", "thinking", "text"}, []string{
		claudeResponse.Content[0].Type,
		claudeResponse.Content[1].Type,
		claudeResponse.Content[2].Type,
		claudeResponse.Content[3].Type,
	})
	require.NotNil(t, claudeResponse.Content[0].Thinking)
	require.NotNil(t, claudeResponse.Content[2].Thinking)
	assert.Equal(t, "**Planning file inspection**", *claudeResponse.Content[0].Thinking)
	assert.Equal(t, "I’ll inspect the starter repository.", claudeResponse.Content[1].GetText())
	assert.Equal(t, "**Clarifying environment task requirements**", *claudeResponse.Content[2].Thinking)
	assert.Equal(t, "What would you like me to build?", claudeResponse.Content[3].GetText())
}

func TestOaiChatToResponsesStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	usage, err := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `event: response.created`)
	require.Contains(t, got, `event: response.output_text.delta`)
	require.Contains(t, got, `"delta":"hello"`)
	require.Contains(t, got, `event: response.function_call_arguments.delta`)
	require.Contains(t, got, `"delta":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `event: response.completed`)
	require.Contains(t, got, `"input_tokens":2`)
	require.Contains(t, got, `"output_tokens":3`)
	requireOrderedSubstrings(t, got,
		`event: response.created`,
		`event: response.output_item.added`,
		`event: response.output_text.delta`,
		`event: response.output_item.added`,
		`event: response.function_call_arguments.delta`,
		`event: response.output_text.done`,
		`event: response.function_call_arguments.done`,
		`event: response.completed`,
	)
}

func TestOaiChatToResponsesStreamHandlerRestoresCodexCustomToolCall(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"exec","arguments":"{\"input\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls -la\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	tools, marshalErr := common.Marshal([]map[string]any{
		{"type": "custom", "name": "exec", "description": "Run a shell command", "format": map[string]any{"type": "text"}},
	})
	require.NoError(t, marshalErr)
	converted, convertErr := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIResponsesToOpenAIChat, &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: []byte(`"list files"`),
		Tools: tools,
	})
	require.NoError(t, convertErr)
	chatRequest, ok := converted.Value.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Tools, 1)
	assert.Equal(t, "function", chatRequest.Tools[0].Type)
	assert.Equal(t, "exec", chatRequest.Tools[0].Function.Name)

	_, err := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)

	got := recorder.Body.String()
	assert.NotContains(t, got, `event: response.function_call_arguments`)
	assert.Contains(t, got, `"delta":"ls -la"`)
	assert.Contains(t, got, `"input":"ls -la"`)
	requireOrderedSubstrings(t, got,
		`event: response.output_item.added`,
		`"type":"custom_tool_call"`,
		`event: response.custom_tool_call_input.delta`,
		`event: response.custom_tool_call_input.done`,
		`event: response.output_item.done`,
		`event: response.completed`,
		`"type":"custom_tool_call"`,
	)
}

func requireOrderedSubstrings(t *testing.T, s string, parts ...string) {
	t.Helper()

	offset := 0
	for _, part := range parts {
		idx := strings.Index(s[offset:], part)
		require.NotEqualf(t, -1, idx, "missing %q after byte offset %d", part, offset)
		offset += idx + len(part)
	}
}

type routingCancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *routingCancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func TestConvertedSSEProviderFailureRetainsRoutingObservation(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, tc := range []struct {
		name      string
		body      string
		handler   func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
		failure   bool
		delivered bool
	}{
		{"buffered_failed", `{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"provider_custom","message":"provider failed"}}}`, OaiResponsesToChatBufferedStreamHandler, true, false},
		{"buffered_error", `{"type":"response.error","response":{"status":"failed","error":{"type":"server_error","code":"provider_custom","message":"provider failed"}}}`, OaiResponsesToChatBufferedStreamHandler, true, false},
		{"buffered_fallback", `{"type":"response.failed"}`, OaiResponsesToChatBufferedStreamHandler, true, false},
		{"stream_failed", `{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"provider_custom","message":"provider failed"}}}`, OaiResponsesToChatStreamHandler, true, false},
		{"stream_error", `{"type":"response.error"}`, OaiResponsesToChatStreamHandler, true, false},
		{"chat_to_responses", `{"error":{"type":"server_error","code":"provider_custom","message":"provider failed"}}`, OaiChatToResponsesStreamHandler, true, false},
		{"buffered_invalid_request", `{"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"provider_custom","message":"provider failed"}}}`, OaiResponsesToChatBufferedStreamHandler, false, false},
		{"buffered_auth", `{"type":"response.failed","response":{"error":{"type":"authentication_error","code":"invalid_api_key","message":"provider failed"}}}`, OaiResponsesToChatBufferedStreamHandler, false, false},
		{"buffered_quota", `{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"insufficient_quota","message":"provider failed"}}}`, OaiResponsesToChatBufferedStreamHandler, false, false},
		{"buffered_rate_limit", `{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"provider failed"}}}`, OaiResponsesToChatBufferedStreamHandler, true, false},
		{"stream_invalid_request", `{"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"provider_custom","message":"provider failed"}}}`, OaiResponsesToChatStreamHandler, false, false},
		{"stream_quota", `{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"insufficient_quota","message":"provider failed"}}}`, OaiResponsesToChatStreamHandler, false, false},
		{"stream_rate_limit", `{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"provider failed"}}}`, OaiResponsesToChatStreamHandler, true, false},
		{"chat_invalid_request", `{"error":{"type":"invalid_request_error","code":"provider_custom","message":"provider failed"}}`, OaiChatToResponsesStreamHandler, false, false},
		{"chat_permission", `{"error":{"type":"permission_error","code":"permission_denied","message":"provider failed"}}`, OaiChatToResponsesStreamHandler, false, false},
		{"chat_quota", `{"error":{"type":"rate_limit_error","code":"insufficient_quota","message":"provider failed"}}`, OaiChatToResponsesStreamHandler, false, false},
		{"chat_rate_limit", `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"provider failed"}}`, OaiChatToResponsesStreamHandler, true, false},
		{"stream_delivered", strings.Join([]string{
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"provider_custom","message":"provider failed"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		}, "\n\ndata: "), OaiResponsesToChatStreamHandler, true, true},
		{"chat_delivered", strings.Join([]string{
			`{"id":"chat-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
			`{"error":{"type":"server_error","code":"provider_custom","message":"provider failed"}}`,
		}, "\n\ndata: "), OaiChatToResponsesStreamHandler, true, true},
		{"stream_invalid_delivered", strings.Join([]string{
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"provider_custom","message":"provider failed"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		}, "\n\ndata: "), OaiResponsesToChatStreamHandler, false, true},
		{"stream_quota_delivered", strings.Join([]string{
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"insufficient_quota","message":"provider failed"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		}, "\n\ndata: "), OaiResponsesToChatStreamHandler, false, true},
		{"stream_rate_limit_delivered", strings.Join([]string{
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"provider failed"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		}, "\n\ndata: "), OaiResponsesToChatStreamHandler, true, true},
		{"chat_invalid_delivered", strings.Join([]string{
			`{"id":"chat-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
			`{"error":{"type":"invalid_request_error","code":"provider_custom","message":"provider failed"}}`,
		}, "\n\ndata: "), OaiChatToResponsesStreamHandler, false, true},
		{"chat_quota_delivered", strings.Join([]string{
			`{"id":"chat-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
			`{"error":{"type":"rate_limit_error","code":"insufficient_quota","message":"provider failed"}}`,
		}, "\n\ndata: "), OaiChatToResponsesStreamHandler, false, true},
		{"chat_rate_limit_delivered", strings.Join([]string{
			`{"id":"chat-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
			`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"provider failed"}}`,
		}, "\n\ndata: "), OaiChatToResponsesStreamHandler, true, true},
		{"buffered_parse_error", `{`, OaiResponsesToChatBufferedStreamHandler, false, false},
		{"chat_parse_error", `{`, OaiChatToResponsesStreamHandler, false, false},
	} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/canceled=%t", tc.name, canceled), func(t *testing.T) {
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				require.NoError(t, db.AutoMigrate(&model.Channel{}))
				require.NoError(t, model.MigrateRoutingMetrics(db))
				previousDB, previousType := model.DB, common.MainDatabaseType()
				model.DB = db
				common.SetMainDatabaseType(common.DatabaseTypeSQLite)
				t.Cleanup(func() {
					assert.NoError(t, service.FlushRoutingMetrics(context.Background()))
					model.DB = previousDB
					common.SetMainDatabaseType(previousType)
					require.NoError(t, sqlDB.Close())
				})
				channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "fixture-key"}
				require.NoError(t, db.Create(channel).Error)
				c, _, resp, info := newResponsesChatTestContext(t, "data: "+tc.body+"\n\n", true)
				c.Set(smartrouting.ContextKey, &model.TokenRouting{Mode: "smart"})
				require.NoError(t, service.BindRoutingOrigin(c, "", "default", channel.Id))
				p := service.RoutingPlanFor(c)
				p.Model, p.Sent = "gpt-test", 1
				c.Set("mowan_routing_send_claimed", true)
				require.NoError(t, service.StartRoutingAttempt(c, "stream"))
				ctx, cancel := context.WithCancel(c.Request.Context())
				c.Request = c.Request.WithContext(ctx)
				t.Cleanup(cancel)
				if canceled && !tc.delivered {
					resp.Body = &routingCancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
				}
				usage, apiErr := tc.handler(c, info, resp)
				if tc.delivered {
					require.Nil(t, apiErr)
					require.NotNil(t, usage)
					require.Equal(t, 5, usage.TotalTokens)
				} else {
					require.NotNil(t, apiErr)
					require.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
					if strings.Contains(tc.body, "provider_custom") {
						require.Equal(t, types.ErrorCode("provider_custom"), apiErr.GetErrorCode())
						require.Equal(t, "provider failed", apiErr.Error())
					}
				}
				if canceled {
					cancel()
					require.ErrorIs(t, c.Request.Context().Err(), context.Canceled)
				}
				service.FinishRoutingSend(c, apiErr, false, 0)
				service.FinishRoutingSend(c, apiErr, false, 0)
				key, err := service.RoutingObservationKey(c, "gpt-test", "default", channel, "stream")
				require.NoError(t, err)
				view, err := (model.RoutingMetricsStore{DB: db}).ReadHealth(key, model.RoutingStageAttempt, 0, time.Time{})
				require.NoError(t, err)
				if tc.failure || (strings.Contains(tc.name, "parse_error") && !canceled) {
					require.EqualValues(t, 1, view.Failures5m)
				} else {
					require.Zero(t, view.Failures5m)
				}
				require.Equal(t, view.Failures5m, view.Eligible5m)
				if strings.Contains(tc.name, "rate_limit") {
					require.EqualValues(t, 1, view.RateLimited60s)
				} else {
					require.Zero(t, view.RateLimited60s)
				}
			})
		}
	}
}
