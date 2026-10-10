package server

import (
	"workbuddy_manager/internal/protocol"
	"workbuddy_manager/internal/session"
)

// bodySessionKey reuses the original protocol's parsed document. A hoisted native
// Chat has different messages, so keep the raw-body fallback for that rare path:
// deriving from inserted user messages would silently change existing sticky keys.
func bodySessionKey(request *protocol.Request, raw []byte) string {
	if request.Source != nil {
		return session.BodyKeyObject(request.Source, request.Chat.PromptCacheKey)
	}
	if !request.Mutated {
		return session.BodyKeyObject(request.Chat.Object, request.Chat.PromptCacheKey)
	}
	return session.BodyKey(raw, request.Chat.PromptCacheKey)
}
