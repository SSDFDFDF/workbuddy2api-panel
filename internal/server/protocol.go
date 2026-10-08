package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	"github.com/linguo2625469/workbuddy2api-panel/internal/httpauth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/protocol"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

func isInferencePath(path string) bool {
	return path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/messages"
}

func (h *Handler) responses(w http.ResponseWriter, r *http.Request) {
	h.inference(w, r, protocol.Responses)
}

func (h *Handler) messages(w http.ResponseWriter, r *http.Request) {
	h.inference(w, r, protocol.Anthropic)
}

// Messages accepts the official x-api-key header or the gateway's Bearer key.
// Conflicting/duplicate credentials are rejected, never tried in succession.
func (h *Handler) withMessagesAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := inferenceOutput{kind: protocol.Anthropic}
		key := h.loadLive().APIKey
		valid := true
		if len(r.Header.Values("X-Api-Key")) > 1 || len(r.Header.Values("Authorization")) > 1 {
			valid = false
		}
		if key != "" {
			x := r.Header.Get("X-Api-Key")
			if x != "" {
				a, b := sha256.Sum256([]byte(x)), sha256.Sum256([]byte(key))
				valid = valid && subtle.ConstantTimeCompare(a[:], b[:]) == 1
				if r.Header.Get("Authorization") != "" {
					valid = valid && httpauth.VerifyBearer(r, key)
				}
			} else {
				valid = valid && httpauth.VerifyBearer(r, key)
			}
		}
		if !valid {
			out.Error(w, 401, "invalid_api_key", "missing or invalid API key")
			return
		}
		if vs := r.Header.Values("Anthropic-Version"); len(vs) != 1 || vs[0] != "2023-06-01" {
			out.Error(w, 400, "invalid_request", "anthropic-version: 2023-06-01 is required")
			return
		}
		if len(r.Header.Values("Anthropic-Beta")) > 0 {
			out.Error(w, 400, "unsupported_parameter", "anthropic-beta features are not supported")
			return
		}
		next(w, r)
	}
}

type inferenceOutput struct {
	kind    protocol.Kind
	request *protocol.Request
}

func (o inferenceOutput) JSON(w http.ResponseWriter, status int, value any) {
	if status >= 400 {
		if body, ok := value.(map[string]any); ok {
			value = protocol.ErrorBody(o.kind, status, body)
		}
	}
	writeJSON(w, status, value)
}

func (o inferenceOutput) Error(w http.ResponseWriter, status int, code, message string) {
	o.ErrorHint(w, status, code, message, "")
}

func (o inferenceOutput) ErrorHint(w http.ResponseWriter, status int, code, message, hint string) {
	if o.kind != protocol.Anthropic {
		writeOpenAIErrorHint(w, status, code, message, hint)
		return
	}
	o.JSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func (o inferenceOutput) Stream(w http.ResponseWriter, r io.Reader, hint func(string) string, opts ...upstream.StreamOption) error {
	if o.kind == protocol.Chat {
		return upstream.StreamHint(w, r, hint, opts...)
	}
	return protocol.Stream(w, r, o.request, hint, opts...)
}

func (o inferenceOutput) Aggregate(r io.Reader, opts ...upstream.StreamOption) (*protocol.Completion, error) {
	if o.kind == protocol.Chat {
		raw, err := upstream.Aggregate(r, opts...)
		if err != nil {
			return nil, err
		}
		return &protocol.Completion{Raw: raw}, nil
	}
	return protocol.Aggregate(r, o.request, opts...)
}

func encodedRequest(req *protocol.Request, original []byte) ([]byte, error) {
	if req.Kind == protocol.Chat {
		return original, nil
	}
	return json.Marshal(req.Chat.Object)
}
