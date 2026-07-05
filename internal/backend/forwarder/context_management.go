package forwarder

import (
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
)

// Context window and trimming constants mirror Claude Code's context.ts and
// autoCompact.ts (see 04f-context-management.md).
const (
	contextAutoCompactBufferTokens   = 13_000
	contextSummaryReserveTokens      = 20_000
	contextMaxConsecutiveCompactFail = 3
	contextMaxPTLRetries               = 3
	contextMaxTooLongRetries           = 2

	contextMaxToolMessageBytes       = 24 * 1024
	contextMaxWorkspaceContextBytes  = 48 * 1024
	contextTrimMessageThreshold      = 35
	contextEarlyTrimFraction         = 0.50
	contextMaxInLoopTailMessages     = 28
)

// effectiveCompiledContextWindow returns the token budget available to the
// agent after reserving headroom for a potential compaction summary request.
func effectiveCompiledContextWindow(conversation *ConversationFile) int64 {
	total := compactionContextWindowSize(conversation)
	reserved := int64(contextSummaryReserveTokens)
	if reserved >= total {
		return total / 2
	}
	return total - reserved
}

// trimBudgetTarget is the proactive token count we trim toward before the
// upstream provider rejects the request.
func trimBudgetTarget(effectiveWindow int64) int64 {
	if effectiveWindow <= 0 {
		return 80_000
	}
	target := int64(float64(effectiveWindow) * contextEarlyTrimFraction)
	if target < 8_000 {
		return 8_000
	}
	return target
}

// shouldTrimCompiledContext returns true when the live prompt should shrink
// proactively — earlier than auto-compact, and also on high message counts.
func shouldTrimCompiledContext(tokensUsed, effectiveWindow int64, messageCount int) bool {
	if messageCount >= contextTrimMessageThreshold {
		return true
	}
	if effectiveWindow <= 0 {
		return messageCount > 20 || tokensUsed > 80_000
	}
	early := trimBudgetTarget(effectiveWindow)
	return tokensUsed >= early || shouldAutoCompactByEstimate(tokensUsed, effectiveWindow, 0)
}

// shouldAutoCompactByEstimate returns true when estimated usage crosses the
// buffer threshold inside the effective window. snipFreed adjusts the estimate
// when a cheap head-snip already dropped tokens this round.
func shouldAutoCompactByEstimate(tokensUsed, effectiveWindow, snipFreed int64) bool {
	if effectiveWindow <= 0 {
		return false
	}
	threshold := effectiveWindow - int64(contextAutoCompactBufferTokens)
	if threshold < 0 {
		threshold = effectiveWindow / 2
	}
	return tokensUsed-snipFreed >= threshold
}

// isContextTooLong reports upstream rejections that mean the prompt must shrink.
func isContextTooLong(err error) bool {
	if err == nil {
		return false
	}
	if isPromptTooLong(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "input is too long") ||
		strings.Contains(msg, "content_length_exceeds") ||
		strings.Contains(msg, "context length exceeded") ||
		strings.Contains(msg, "request too large") ||
		strings.Contains(msg, "maximum context") ||
		strings.Contains(msg, "token limit")
}

// isPromptTooLong reports compaction-summary and provider rejections that mean
// the summarization input itself must shrink.
func isPromptTooLong(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "prompt too long") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "maximum context") ||
		strings.Contains(msg, "token limit") ||
		strings.Contains(msg, "413")
}

type contextTrimResult struct {
	Trimmed     bool
	TokensFreed int64
	SnipFreed   int64
}

// manageCompiledContextBeforeProvider applies cheap snip and emergency trim
// when the live message list approaches the effective window. Trimming only
// affects the ephemeral provider request — persisted history entries are not
// rewritten.
func manageCompiledContextBeforeProvider(compiled CompiledConversation, conversation *ConversationFile, aggressive bool) (CompiledConversation, contextTrimResult) {
	var result contextTrimResult
	if len(compiled.Messages) < 2 {
		return compiled, result
	}
	effective := effectiveCompiledContextWindow(conversation)
	tokensUsed := estimateCompiledPromptTokens(compiled)
	targetTokens := trimBudgetTarget(effective)
	if aggressive {
		targetTokens = effective / 3
		if targetTokens < 4_000 {
			targetTokens = 4_000
		}
	}
	prefixLen := compiledPromptPrefixLen(compiled)
	if !shouldTrimCompiledContext(tokensUsed, effective, len(compiled.Messages)) && !aggressive {
		return compiled, result
	}
	trimmed := emergencyTrimCompiledMessages(compiled.Messages, prefixLen, targetTokens)
	after := estimateModelMessagesTokens(trimmed)
	if after < tokensUsed {
		result.Trimmed = true
		result.TokensFreed = tokensUsed - after
		compiled.Messages = trimmed
		tokensUsed = after
	}
	if shouldAutoCompactByEstimate(tokensUsed, effective, 0) {
		compactTarget := effective - int64(contextAutoCompactBufferTokens)
		if compactTarget < effective/2 {
			compactTarget = effective / 2
		}
		snipped, freed := cheapSnipOldCompiledMessages(compiled.Messages, prefixLen, compactTarget)
		if freed > 0 {
			compiled.Messages = snipped
			result.SnipFreed += freed
			result.Trimmed = true
		}
	}
	return compiled, result
}

func compiledPromptPrefixLen(compiled CompiledConversation) int {
	stable := compiled.StableMessageCount
	if stable < 0 {
		stable = 0
	}
	return 1 + stable
}

// cheapSnipOldCompiledMessages drops the oldest replayed history messages
// (after the static system prefix) until estimated tokens fall below target
// or no more history remains. Returns tokens freed (estimate).
func cheapSnipOldCompiledMessages(messages []modeladapter.Message, prefixLen int, targetTokens int64) ([]modeladapter.Message, int64) {
	if len(messages) < 2 || prefixLen < 2 {
		return messages, 0
	}
	before := estimateModelMessagesTokens(messages)
	histStart := 1
	if histStart >= len(messages) {
		return messages, 0
	}
	histEnd := prefixLen
	if histEnd > len(messages) {
		histEnd = len(messages)
	}
	if histEnd <= histStart {
		return messages, 0
	}
	out := append([]modeladapter.Message(nil), messages[:histStart]...)
	hist := append([]modeladapter.Message(nil), messages[histStart:histEnd]...)
	tail := append([]modeladapter.Message(nil), messages[histEnd:]...)

	for len(hist) > 0 && estimateModelMessagesTokens(append(append(out, hist...), tail...)) > targetTokens {
		drop := 1
		if len(hist) >= 2 {
			drop = 2
		}
		hist = hist[drop:]
	}
	result := append(out, hist...)
	result = append(result, tail...)
	after := estimateModelMessagesTokens(result)
	freed := before - after
	if freed < 0 {
		freed = 0
	}
	return result, freed
}

// emergencyTrimCompiledMessages aggressively shrinks the live prompt: cap tool
// payloads, shrink workspace blocks, snip old history, then drop in-loop tail
// rounds until under targetTokens.
func emergencyTrimCompiledMessages(messages []modeladapter.Message, prefixLen int, targetTokens int64) []modeladapter.Message {
	if len(messages) < 2 || targetTokens <= 0 {
		return messages
	}
	out := truncateAllCompiledToolMessages(messages)
	out = capCompiledWorkspaceMessages(out)

	if estimateModelMessagesTokens(out) <= targetTokens {
		return out
	}
	snipped, _ := cheapSnipOldCompiledMessages(out, prefixLen, targetTokens)
	out = snipped

	for keep := contextMaxInLoopTailMessages; keep >= 4 && estimateModelMessagesTokens(out) > targetTokens; keep -= 4 {
		out = trimCompiledInLoopTail(out, prefixLen, keep)
	}
	if estimateModelMessagesTokens(out) > targetTokens {
		out = trimCompiledInLoopTail(out, prefixLen, 2)
	}
	return out
}

func truncateCompiledToolPayload(msg modeladapter.Message, maxBytes int) modeladapter.Message {
	if strings.TrimSpace(msg.Role) != "tool" || maxBytes <= 0 {
		return msg
	}
	text := strings.TrimSpace(msg.Content)
	if len(text) <= maxBytes {
		return msg
	}
	marker := "\n…[tool output truncated — call Read on the file or re-run with a narrower scope to see the rest]"
	cut := maxBytes - len(marker)
	if cut < 256 {
		cut = maxBytes
		marker = ""
	}
	out := msg
	out.Content = text[:cut] + marker
	return out
}

func truncateAllCompiledToolMessages(messages []modeladapter.Message) []modeladapter.Message {
	out := make([]modeladapter.Message, len(messages))
	for i, m := range messages {
		out[i] = truncateCompiledToolPayload(m, contextMaxToolMessageBytes)
	}
	return out
}

func capCompiledWorkspaceMessages(messages []modeladapter.Message) []modeladapter.Message {
	if len(messages) == 0 {
		return messages
	}
	out := append([]modeladapter.Message(nil), messages...)
	for i := range out {
		if strings.TrimSpace(out[i].Role) != "user" {
			continue
		}
		text := strings.TrimSpace(out[i].Content)
		if text == "" || !strings.Contains(text, "<user_info>") {
			continue
		}
		if len(text) <= contextMaxWorkspaceContextBytes {
			continue
		}
		marker := "\n…[workspace context truncated to fit model limit]"
		cut := contextMaxWorkspaceContextBytes - len(marker)
		if cut < 1024 {
			cut = contextMaxWorkspaceContextBytes
			marker = ""
		}
		out[i].Content = text[:cut] + marker
	}
	return out
}

func trimCompiledInLoopTail(messages []modeladapter.Message, prefixLen, maxKeep int) []modeladapter.Message {
	if maxKeep <= 0 || prefixLen <= 0 || len(messages) <= prefixLen {
		return messages
	}
	tail := messages[prefixLen:]
	if len(tail) <= maxKeep {
		return messages
	}
	drop := len(tail) - maxKeep
	out := append([]modeladapter.Message(nil), messages[:prefixLen]...)
	out = append(out, tail[drop:]...)
	return out
}

func stripImagesFromCompactionText(text string) string {
	for {
		start := strings.Index(text, "data:image/")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], ";base64,")
		if end < 0 {
			break
		}
		rest := text[start+end+8:]
		close := strings.IndexAny(rest, "\"'\n> ")
		if close < 0 {
			text = text[:start] + "[image]" + rest
		} else {
			text = text[:start] + "[image]" + rest[close:]
		}
	}
	return text
}

func peelCompactionSummaryInputForPTL(text string) (string, bool) {
	cut := len(text) / 5
	if cut < 512 {
		return text, false
	}
	return text[cut:], true
}

func autoCompactionCircuitOpen(conversation *ConversationFile) bool {
	if conversation == nil {
		return false
	}
	return conversation.AutoCompactionConsecutiveFailures >= contextMaxConsecutiveCompactFail
}

func recordAutoCompactionSuccess(conversation *ConversationFile) {
	if conversation == nil {
		return
	}
	conversation.AutoCompactionConsecutiveFailures = 0
}

func recordAutoCompactionFailure(conversation *ConversationFile) {
	if conversation == nil {
		return
	}
	conversation.AutoCompactionConsecutiveFailures++
}

func contextUsagePercent(tokensUsed, effectiveWindow int64) float64 {
	if effectiveWindow <= 0 {
		return 0
	}
	pct := float64(tokensUsed) / float64(effectiveWindow) * 100
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}
