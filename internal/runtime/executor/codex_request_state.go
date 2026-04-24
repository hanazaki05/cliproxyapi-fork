package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor/helps"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	codexConversationIDMetadataKey = "conversation_id"
	codexIdempotencyMetadataKey    = "idempotency_key"
)

type codexRequestState struct {
	ConversationID string
	TurnID         string
	TurnState      string
	TurnMetadata   string
	Subagent       string
	InstallationID string
	WindowID       string
}

func prepareCodexRequestState(ctx context.Context, opts cliproxyexecutor.Options, from sdktranslator.Format, req cliproxyexecutor.Request, rawJSON []byte) (codexRequestState, []byte) {
	ginHeaders := codexGinHeadersFromContext(ctx)
	state := codexRequestState{
		ConversationID: resolveCodexConversationID(ctx, opts, from, req, rawJSON, ginHeaders),
		TurnID:         resolveCodexTurnID(opts, ginHeaders),
		TurnState:      resolveCodexTurnState(opts, ginHeaders),
		Subagent:       resolveCodexSubagent(opts, ginHeaders),
		InstallationID: firstNonEmptyHeader(ginHeaders, "X-Codex-Installation-Id", ""),
		WindowID:       firstNonEmptyHeader(ginHeaders, "X-Codex-Window-Id", ""),
	}

	if state.ConversationID != "" {
		if updated, err := sjson.SetBytes(rawJSON, "prompt_cache_key", state.ConversationID); err == nil {
			rawJSON = updated
		}
	}

	state.TurnMetadata = resolveCodexTurnMetadataHeader(firstNonEmptyHeader(ginHeaders, "X-Codex-Turn-Metadata", ""), state.ConversationID, state.TurnID)
	return state, rawJSON
}

func applyCodexRequestStateHeaders(headers http.Header, state codexRequestState) {
	if headers == nil {
		return
	}
	if state.ConversationID != "" {
		headers.Set("Conversation_id", state.ConversationID)
		headers.Set("Session_id", state.ConversationID)
		headers.Set("X-Client-Request-Id", state.ConversationID)
	}
	if state.TurnState != "" {
		headers.Set("X-Codex-Turn-State", state.TurnState)
	}
	if state.TurnMetadata != "" {
		headers.Set("X-Codex-Turn-Metadata", state.TurnMetadata)
	}
	if state.Subagent != "" {
		headers.Set("X-OpenAI-Subagent", state.Subagent)
	}
	if state.InstallationID != "" {
		headers.Set("X-Codex-Installation-Id", state.InstallationID)
	}
	if state.WindowID != "" {
		headers.Set("X-Codex-Window-Id", state.WindowID)
	}
}

func ensureCodexConversationTurnHeaders(target http.Header, fallback http.Header) {
	if target == nil {
		return
	}

	conversationID := resolveCodexConversationIDFromHeaders(target, fallback)
	if conversationID == "" {
		conversationID = uuid.NewString()
	}
	target.Set("Conversation_id", conversationID)
	target.Set("Session_id", conversationID)
	target.Set("X-Client-Request-Id", conversationID)

	if turnState := resolveCodexTurnStateFromHeaders(target, fallback); turnState != "" {
		target.Set("X-Codex-Turn-State", turnState)
	}
	if subagent := resolveCodexSubagentFromHeaders(target, fallback); subagent != "" {
		target.Set("X-OpenAI-Subagent", subagent)
	}
	if installationID := firstNonEmptyHeader(target, "X-Codex-Installation-Id", ""); installationID != "" {
		target.Set("X-Codex-Installation-Id", installationID)
	} else if installationID = firstNonEmptyHeader(fallback, "X-Codex-Installation-Id", ""); installationID != "" {
		target.Set("X-Codex-Installation-Id", installationID)
	}
	if windowID := firstNonEmptyHeader(target, "X-Codex-Window-Id", ""); windowID != "" {
		target.Set("X-Codex-Window-Id", windowID)
	} else if windowID = firstNonEmptyHeader(fallback, "X-Codex-Window-Id", ""); windowID != "" {
		target.Set("X-Codex-Window-Id", windowID)
	}

	turnID := resolveCodexTurnIDFromHeaders(target, fallback)
	if turnID == "" {
		turnID = uuid.NewString()
	}
	turnMetadata := resolveCodexTurnMetadataHeader(firstNonEmptyHeader(target, "X-Codex-Turn-Metadata", ""), conversationID, turnID)
	if turnMetadata == "" {
		turnMetadata = resolveCodexTurnMetadataHeader(firstNonEmptyHeader(fallback, "X-Codex-Turn-Metadata", ""), conversationID, turnID)
	}
	if turnMetadata != "" {
		target.Set("X-Codex-Turn-Metadata", turnMetadata)
	}
}

func codexGinHeadersFromContext(ctx context.Context) http.Header {
	if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
		return ginCtx.Request.Header
	}
	return nil
}

func resolveCodexConversationID(ctx context.Context, opts cliproxyexecutor.Options, from sdktranslator.Format, req cliproxyexecutor.Request, rawJSON []byte, ginHeaders http.Header) string {
	if conversationID := strings.TrimSpace(stringMetadata(opts.Metadata, codexConversationIDMetadataKey)); conversationID != "" {
		return conversationID
	}
	if executionSessionID := executionSessionIDFromOptions(opts); executionSessionID != "" {
		return executionSessionID
	}
	if promptCacheKey := codexPromptCacheKeyFromPayload(rawJSON, req.Payload); promptCacheKey != "" {
		return promptCacheKey
	}
	if conversationID := resolveCodexConversationIDFromHeaders(nil, ginHeaders); conversationID != "" {
		return conversationID
	}
	if from == "claude" {
		userIDResult := gjson.GetBytes(req.Payload, "metadata.user_id")
		if userIDResult.Exists() {
			key := req.Model + "-" + userIDResult.String()
			if cache, ok := helps.GetCodexCache(key); ok && strings.TrimSpace(cache.ID) != "" {
				return cache.ID
			}
			cache := helps.CodexCache{ID: uuid.NewString(), Expire: time.Now().Add(time.Hour)}
			helps.SetCodexCache(key, cache)
			return cache.ID
		}
	}
	if from == "openai" {
		if apiKey := strings.TrimSpace(helps.APIKeyFromContext(ctx)); apiKey != "" {
			return uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex:prompt-cache:"+apiKey)).String()
		}
	}
	if idempotencyKey := strings.TrimSpace(stringMetadata(opts.Metadata, codexIdempotencyMetadataKey)); idempotencyKey != "" {
		return idempotencyKey
	}
	return uuid.NewString()
}

func resolveCodexConversationIDFromHeaders(primary http.Header, fallback http.Header) string {
	for _, key := range []string{"Conversation_id", "Session_id", "X-Client-Request-Id"} {
		if value := firstNonEmptyHeader(primary, key, ""); value != "" {
			return value
		}
		if value := firstNonEmptyHeader(fallback, key, ""); value != "" {
			return value
		}
	}
	return ""
}

func resolveCodexTurnID(opts cliproxyexecutor.Options, ginHeaders http.Header) string {
	if turnID := strings.TrimSpace(stringMetadata(opts.Metadata, cliproxyexecutor.TurnIDMetadataKey)); turnID != "" {
		return turnID
	}
	if turnID := turnIDFromMetadataHeader(firstNonEmptyHeader(ginHeaders, "X-Codex-Turn-Metadata", "")); turnID != "" {
		return turnID
	}
	if idempotencyKey := strings.TrimSpace(stringMetadata(opts.Metadata, codexIdempotencyMetadataKey)); idempotencyKey != "" {
		return idempotencyKey
	}
	return uuid.NewString()
}

func resolveCodexTurnIDFromHeaders(primary http.Header, fallback http.Header) string {
	if turnID := turnIDFromMetadataHeader(firstNonEmptyHeader(primary, "X-Codex-Turn-Metadata", "")); turnID != "" {
		return turnID
	}
	if turnID := turnIDFromMetadataHeader(firstNonEmptyHeader(fallback, "X-Codex-Turn-Metadata", "")); turnID != "" {
		return turnID
	}
	return ""
}

func resolveCodexTurnState(opts cliproxyexecutor.Options, ginHeaders http.Header) string {
	if turnState := strings.TrimSpace(stringMetadata(opts.Metadata, cliproxyexecutor.TurnStateMetadataKey)); turnState != "" {
		return turnState
	}
	return firstNonEmptyHeader(ginHeaders, "X-Codex-Turn-State", "")
}

func resolveCodexTurnStateFromHeaders(primary http.Header, fallback http.Header) string {
	if turnState := firstNonEmptyHeader(primary, "X-Codex-Turn-State", ""); turnState != "" {
		return turnState
	}
	return firstNonEmptyHeader(fallback, "X-Codex-Turn-State", "")
}

func resolveCodexSubagent(opts cliproxyexecutor.Options, ginHeaders http.Header) string {
	if subagent := strings.TrimSpace(stringMetadata(opts.Metadata, cliproxyexecutor.SubagentMetadataKey)); subagent != "" {
		return subagent
	}
	return firstNonEmptyHeader(ginHeaders, "X-OpenAI-Subagent", "")
}

func resolveCodexSubagentFromHeaders(primary http.Header, fallback http.Header) string {
	if subagent := firstNonEmptyHeader(primary, "X-OpenAI-Subagent", ""); subagent != "" {
		return subagent
	}
	return firstNonEmptyHeader(fallback, "X-OpenAI-Subagent", "")
}

func resolveCodexTurnMetadataHeader(existingHeader string, conversationID string, turnID string) string {
	payload := map[string]any{}
	if strings.TrimSpace(existingHeader) != "" {
		if err := json.Unmarshal([]byte(existingHeader), &payload); err != nil {
			payload = map[string]any{}
		}
	}
	if conversationID != "" {
		payload["session_id"] = conversationID
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}
	if sandbox := detectCodexSandboxTag(); sandbox != "" {
		if _, exists := payload["sandbox"]; !exists {
			payload["sandbox"] = sandbox
		}
	}
	if len(payload) == 0 {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func detectCodexSandboxTag() string {
	for _, key := range []string{"CODEX_SANDBOX_MODE", "CODEX_SANDBOX"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func codexPromptCacheKeyFromPayload(payloads ...[]byte) string {
	for _, payload := range payloads {
		if promptCacheKey := strings.TrimSpace(gjson.GetBytes(payload, "prompt_cache_key").String()); promptCacheKey != "" {
			return promptCacheKey
		}
	}
	return ""
}

func turnIDFromMetadataHeader(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	return strings.TrimSpace(gjson.Get(header, "turn_id").String())
}

func stringMetadata(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case []byte:
		return strings.TrimSpace(string(value))
	default:
		return ""
	}
}

func firstNonEmptyHeader(headers http.Header, name, fallback string) string {
	if headers == nil {
		return strings.TrimSpace(fallback)
	}
	if value := strings.TrimSpace(headers.Get(name)); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func codexRequestAlignEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.RequestAlignCodex
}
