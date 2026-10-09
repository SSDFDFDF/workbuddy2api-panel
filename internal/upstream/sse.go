// SSE decoding and completion state are shared by streaming and aggregation.
package upstream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"workbuddy_manager/internal/jsondoc"
)

const maxSSEEvent = 8 << 20
const maxAggregate = 64 << 20

var errEmptyStream = errors.New("upstream stream contained no completion choices")
var errTruncatedStream = errors.New("upstream stream ended without all choice finish reasons")

func IsEmptyStreamError(err error) bool { return errors.Is(err, errEmptyStream) }

type FrameError struct {
	Payload string
	Kind    ErrKind
	Status  int
}

func (e *FrameError) Error() string { return e.Payload }

// DownstreamWriteError distinguishes delivery failure from upstream read failure.
type DownstreamWriteError struct{ Err error }

func (e *DownstreamWriteError) Error() string { return e.Err.Error() }
func (e *DownstreamWriteError) Unwrap() error { return e.Err }

type sseEvent struct{ Data, Event, ID, Retry string }
type eventReader struct{ br *bufio.Reader }

func (r *eventReader) next() (sseEvent, error) {
	var e sseEvent
	var data []string
	size := 0
	for {
		// ReadSlice bounds allocation even for a hostile stream without newline.
		var line []byte
		for {
			p, err := r.br.ReadSlice('\n')
			size += len(p)
			if size > maxSSEEvent {
				return e, fmt.Errorf("SSE event exceeds %d bytes", maxSSEEvent)
			}
			line = append(line, p...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				if err != io.EOF {
					return e, fmt.Errorf("read upstream: %w", err)
				}
				if len(line) > 0 || len(data) > 0 {
					return e, errTruncatedStream
				}
				return e, io.EOF
			}
			break
		}
		s := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if s == "" {
			if len(data) == 0 {
				e = sseEvent{}
				size = 0
				continue
			}
			e.Data = strings.Join(data, "\n")
			return e, nil
		}
		if strings.HasPrefix(s, ":") {
			continue
		}
		k, v, _ := strings.Cut(s, ":")
		v = strings.TrimPrefix(v, " ")
		switch k {
		case "data":
			data = append(data, v)
		case "event":
			e.Event = v
		case "id":
			if !strings.ContainsRune(v, 0) {
				e.ID = v
			}
		case "retry":
			e.Retry = v
		}
	}
}

type completionState struct {
	top               map[string]any
	choices           map[int]*choiceState
	expected          int
	aggregate         bool
	bytes             int
	emptyToolIdentity bool
	declaredToolNames map[string]bool
}
type choiceState struct {
	fields            map[string]any
	message           map[string]any
	tools             map[int]map[string]any
	finish            string
	emptyToolIdentity bool
	declaredToolNames map[string]bool
	toolNames         map[int]*toolNameState
	messageStrings    map[string]*strings.Builder
	toolStrings       map[int]map[string]*strings.Builder
}

func newCompletion(n int, aggregate bool) *completionState {
	if n <= 0 {
		n = 1
	}
	return &completionState{top: map[string]any{}, choices: map[int]*choiceState{}, expected: n, aggregate: aggregate}
}

func mergeStable(dst, src map[string]any, ignore map[string]bool, strict bool) error {
	for k, v := range src {
		if ignore[k] {
			continue
		}
		old, has := dst[k]
		if !has || old == nil {
			dst[k] = v
			continue
		}
		if v == nil || reflect.DeepEqual(old, v) {
			continue
		}
		a, aok := old.(map[string]any)
		b, bok := v.(map[string]any)
		if aok && bok {
			if err := mergeStable(a, b, nil, strict); err != nil {
				return err
			}
			continue
		}
		if strict {
			return fmt.Errorf("response_not_aggregatable: conflicting field %s", k)
		}
	}
	return nil
}

// appendString keeps a growing buffer instead of copying the entire prefix on
// every frame. Builders stay behind pointers (a non-zero Builder must not be
// copied). String() exposes an immutable prefix without a second allocation;
// subsequent appends never overwrite bytes already visible to maps/observers.
func appendString(dst map[string]any, buffers map[string]*strings.Builder, k string, v any) error {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("invalid string delta %s", k)
	}
	b := buffers[k]
	if b == nil {
		b = &strings.Builder{}
		old, _ := dst[k].(string)
		b.WriteString(old)
		buffers[k] = b
	}
	b.WriteString(s)
	dst[k] = b.String()
	return nil
}
func mergeList(dst map[string]any, k string, v any) error {
	if v == nil {
		return nil
	}
	a, ok := v.([]any)
	if !ok {
		return fmt.Errorf("invalid array delta %s", k)
	}
	old, _ := dst[k].([]any)
	dst[k] = append(old, a...)
	return nil
}
func (c *choiceState) mergeMessage(m map[string]any, snapshot, strict bool) error {
	if snapshot { // Full messages are snapshots, never a second copy of deltas.
		for k, v := range m {
			if old, ok := c.message[k]; ok && old != nil && old != "" && !reflect.DeepEqual(old, v) {
				return fmt.Errorf("conflicting message snapshot field %s", k)
			}
			c.message[k] = v
			// A snapshot may replace empty/null fields. Re-seed any later
			// delta from that exact snapshot, not a stale buffer.
			delete(c.messageStrings, k)
		}
		return nil
	}
	for _, k := range []string{"content", "reasoning", "reasoning_content", "refusal"} {
		if v, ok := m[k]; ok {
			if err := appendString(c.message, c.messageStrings, k, v); err != nil {
				return err
			}
		}
	}
	if v, ok := m["annotations"]; ok {
		if err := mergeList(c.message, "annotations", v); err != nil {
			return err
		}
	}
	if raw, ok := m["tool_calls"]; ok && raw != nil {
		calls, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("invalid tool_calls array")
		}
		for _, v := range calls {
			d, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid tool call delta")
			}
			idx := -1
			if n, ok := jsondoc.Int(d["index"]); ok && n >= 0 && n < 128 {
				idx = int(n)
			} else if _, present := d["index"]; present {
				return fmt.Errorf("invalid tool index")
			}
			id, _ := d["id"].(string)
			if idx < 0 && id != "" {
				for i, t := range c.tools {
					if t["id"] == id {
						idx = i
						break
					}
				}
				if idx < 0 {
					idx = len(c.tools)
					for c.tools[idx] != nil {
						idx++
					}
				}
			}
			if idx < 0 {
				if len(c.tools) != 1 {
					return fmt.Errorf("ambiguous tool delta without index or id")
				}
				for i := range c.tools {
					idx = i
				}
			}
			t := c.tools[idx]
			if t == nil {
				t = map[string]any{}
				c.tools[idx] = t
			}
			identity := d
			if c.emptyToolIdentity && (d["id"] == "" || d["type"] == "") {
				// Some OpenAI-compatible endpoints send empty identity strings
				// on continuation chunks. An explicit index identifies the call;
				// empty strings contain no new identity and must not erase it.
				// Non-empty conflicts still fail in mergeStable below.
				identity = make(map[string]any, len(d))
				for k, v := range d {
					if (k == "id" || k == "type") && v == "" {
						continue
					}
					identity[k] = v
				}
			}
			if err := mergeStable(t, identity, map[string]bool{"index": true, "function": true}, true); err != nil {
				return err
			}
			if raw, ok := d["function"]; ok {
				fn, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid tool function")
				}
				to, _ := t["function"].(map[string]any)
				if to == nil {
					to = map[string]any{}
					t["function"] = to
				}
				if c.toolStrings[idx] == nil {
					c.toolStrings[idx] = map[string]*strings.Builder{}
				}
				// Native adapter follows incremental name semantics, including delayed names.
				for _, k := range []string{"name", "arguments"} {
					if v, ok := fn[k]; ok {
						if k == "name" && c.declaredToolNames != nil {
							if c.toolNames[idx] == nil {
								c.toolNames[idx] = &toolNameState{declared: c.declaredToolNames}
							}
							if err := c.toolNames[idx].add(v); err != nil {
								return err
							}
						} else if err := appendString(to, c.toolStrings[idx], k, v); err != nil {
							return err
						}
					}
				}
				if err := mergeStable(to, fn, map[string]bool{"name": true, "arguments": true}, strict); err != nil {
					return err
				}
			}
		}
	}
	return mergeStable(c.message, m, map[string]bool{"content": true, "reasoning": true, "reasoning_content": true, "refusal": true, "annotations": true, "tool_calls": true}, strict)
}
func (s *completionState) add(obj map[string]any) error {
	for _, k := range []string{"id", "model"} {
		if v, exists := obj[k]; exists && v != nil {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("invalid completion %s", k)
			}
		}
		if old, ok := s.top[k]; ok && obj[k] != nil && obj[k] != "" && old != obj[k] {
			return fmt.Errorf("completion %s changed mid-stream", k)
		}
	}
	if err := mergeStable(s.top, obj, map[string]bool{"choices": true, "usage": true, "object": true}, s.aggregate); err != nil {
		return err
	}
	if u, ok := obj["usage"]; ok && u != nil {
		s.top["usage"] = u
	}
	raw, exists := obj["choices"]
	if !exists {
		return fmt.Errorf("upstream event has no choices")
	}
	choices, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("invalid choices array")
	}
	seen := map[int]bool{}
	for _, v := range choices {
		ch, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid choice")
		}
		n, ok := jsondoc.Int(ch["index"])
		if !ok || n < 0 || n >= int64(s.expected) || seen[int(n)] {
			return fmt.Errorf("invalid or duplicate choice index")
		}
		i := int(n)
		seen[i] = true
		c := s.choices[i]
		if c == nil {
			c = &choiceState{fields: map[string]any{"index": i}, message: map[string]any{}, tools: map[int]map[string]any{}, emptyToolIdentity: s.emptyToolIdentity,
				declaredToolNames: s.declaredToolNames, toolNames: map[int]*toolNameState{},
				messageStrings: map[string]*strings.Builder{}, toolStrings: map[int]map[string]*strings.Builder{}}
			s.choices[i] = c
		}
		if c.finish != "" {
			return fmt.Errorf("choice emitted after finish")
		}
		if d, has := ch["delta"]; has {
			m, ok := d.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid choice delta")
			}
			if err := c.mergeMessage(m, false, s.aggregate); err != nil {
				return err
			}
		}
		if d, has := ch["message"]; has {
			m, ok := d.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid choice message")
			}
			if err := c.mergeMessage(m, true, s.aggregate); err != nil {
				return err
			}
		}
		if raw, has := ch["logprobs"]; has && raw != nil {
			m, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid logprobs")
			}
			to, _ := c.fields["logprobs"].(map[string]any)
			if to == nil {
				to = map[string]any{}
				c.fields["logprobs"] = to
			}
			for _, k := range []string{"content", "refusal"} {
				if v, ok := m[k]; ok {
					if err := mergeList(to, k, v); err != nil {
						return err
					}
				}
			}
			if err := mergeStable(to, m, map[string]bool{"content": true, "refusal": true}, s.aggregate); err != nil {
				return err
			}
		}
		if err := mergeStable(c.fields, ch, map[string]bool{"index": true, "delta": true, "message": true, "finish_reason": true, "logprobs": true}, s.aggregate); err != nil {
			return err
		}
		if f, ok := ch["finish_reason"]; ok && f != nil {
			str, ok := f.(string)
			if !ok {
				return fmt.Errorf("invalid finish_reason")
			}
			c.finish = str
		}
		if c.finish == "tool_calls" {
			if err := c.validateTools(); err != nil {
				return err
			}
		} else if c.declaredToolNames != nil && (c.finish == "length" || c.finish == "content_filter") {
			// Resolve only observed complete names for translating consumers.
			// The protocol layer decides whether partial arguments can be represented.
			if err := c.resolveToolNames(); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *choiceState) resolveToolNames() error {
	for index, state := range c.toolNames {
		name, err := state.name()
		if err != nil {
			return err
		}
		c.tools[index]["function"].(map[string]any)["name"] = name
	}
	return nil
}
func (c *choiceState) validateTools() error {
	if err := c.resolveToolNames(); err != nil {
		return err
	}
	calls := []any{}
	if len(c.tools) > 0 {
		for _, t := range c.tools {
			calls = append(calls, t)
		}
	} else {
		calls, _ = c.message["tool_calls"].([]any)
	}
	if len(calls) == 0 {
		return fmt.Errorf("tool_calls finish without calls")
	}
	for _, v := range calls {
		t, _ := v.(map[string]any)
		fn, _ := t["function"].(map[string]any)
		name, _ := fn["name"].(string)
		id, _ := t["id"].(string)
		args, ok := fn["arguments"].(string)
		if name == "" || id == "" || !ok {
			return fmt.Errorf("incomplete tool call")
		}
		if c.declaredToolNames != nil && !c.declaredToolNames[name] {
			return fmt.Errorf("upstream tool name does not match declared tools")
		}
		if _, err := jsondoc.Decode([]byte(args)); err != nil {
			return fmt.Errorf("invalid completed tool arguments: %w", err)
		}
	}
	return nil
}
func (s *completionState) complete() error {
	if len(s.choices) == 0 {
		return errEmptyStream
	}
	if len(s.choices) != s.expected {
		return errTruncatedStream
	}
	for _, c := range s.choices {
		if c.finish == "" {
			return errTruncatedStream
		}
	}
	return nil
}
func (s *completionState) response() map[string]any {
	out := s.top
	out["object"] = "chat.completion"
	if out["id"] == nil || out["id"] == "" {
		out["id"] = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	if out["created"] == nil {
		out["created"] = time.Now().Unix()
	}
	ids := []int{}
	for i := range s.choices {
		ids = append(ids, i)
	}
	sort.Ints(ids)
	chs := []any{}
	for _, i := range ids {
		c := s.choices[i]
		if _, ok := c.message["role"]; !ok {
			c.message["role"] = "assistant"
		}
		if _, ok := c.message["content"]; !ok {
			c.message["content"] = nil
		}
		if len(c.tools) > 0 {
			idxs := []int{}
			for j := range c.tools {
				idxs = append(idxs, j)
			}
			sort.Ints(idxs)
			a := []any{}
			for _, j := range idxs {
				a = append(a, c.tools[j])
			}
			c.message["tool_calls"] = a
		}
		c.fields["message"] = c.message
		c.fields["finish_reason"] = c.finish
		chs = append(chs, c.fields)
	}
	out["choices"] = chs
	if u, ok := out["usage"].(map[string]any); ok {
		out["usage"] = ensureUsageTotal(u)
	}
	return out
}
func ensureUsageTotal(u map[string]any) map[string]any {
	if _, ok := u["total_tokens"]; ok {
		return u
	}
	p, ok := jsondoc.Int(u["prompt_tokens"])
	c, yes := jsondoc.Int(u["completion_tokens"])
	if ok && yes && p >= 0 && c >= 0 && p <= int64(^uint64(0)>>1)-c {
		out := map[string]any{}
		for k, v := range u {
			out[k] = v
		}
		out["total_tokens"] = p + c
		return out
	}
	return u
}

// errorFrame uses the same classification for HTTP errors and stream errors.
func errorFrame(obj map[string]any, payload, event string) *FrameError {
	_, has := obj["error"]
	code, codePresent := obj["code"]
	business := codePresent && code != nil && fmt.Sprint(code) != "0"
	if !has && !business && event != "error" {
		return nil
	}
	kind := Classify(400, payload)
	if hasBusinessCode(payload, "6004") {
		kind = ErrSoftRate
	}
	return &FrameError{payload, kind, ErrorStatus(kind)}
}
func ErrorStatus(k ErrKind) int {
	switch k {
	case ErrSoftRate:
		return 429
	case ErrBadParams, ErrClient, ErrImageInvalid, ErrPromptTooLong, ErrContentBlocked, ErrModelBlocked:
		return 400
	case ErrHardCredit, ErrAccountFault, ErrSessionDead:
		return 503
	default:
		return 502
	}
}

type StreamOption func(*streamOptions)
type streamOptions struct {
	onErrorFrame      func(string)
	onFrame           func(map[string]any)
	expected          int
	firstEvent        time.Duration
	firstGeneration   time.Duration
	tail              time.Duration
	emptyToolIdentity bool
	declaredToolNames map[string]bool
}

func WithErrorFrameObserver(fn func(string)) StreamOption {
	return func(o *streamOptions) { o.onErrorFrame = fn }
}
func WithFrameObserver(fn func(map[string]any)) StreamOption {
	return func(o *streamOptions) { o.onFrame = fn }
}
func WithExpectedChoices(n int) StreamOption { return func(o *streamOptions) { o.expected = n } }

// WithEmptyToolIdentityDeltas opts a translating consumer into treating empty
// identity fields as absent. No identity is invented and conflicting non-empty
// values or ambiguous deltas remain errors. Native Chat stays unchanged.
func WithEmptyToolIdentityDeltas() StreamOption {
	return func(o *streamOptions) { o.emptyToolIdentity = true }
}

// watchResponse closes a blocked upstream reader when semantic deadlines expire.
// Heartbeats do not extend these deadlines. All timer ownership ends with consume.
func watchResponse(r io.Reader, o streamOptions) (func(*completionState, map[string]any), func(), func() error) {
	closer, ok := r.(io.Closer)
	if !ok {
		return func(*completionState, map[string]any) {}, func() {}, func() error { return nil }
	}
	first, generation, tail := o.firstEvent, o.firstGeneration, o.tail
	if first <= 0 {
		first = 120 * time.Second
	}
	if generation <= 0 {
		generation = 300 * time.Second
	}
	if tail <= 0 {
		tail = 10 * time.Second
	}
	var mu sync.Mutex
	var failure error
	stopped := false
	expire := func(stage string) {
		mu.Lock()
		if stopped {
			mu.Unlock()
			return
		}
		failure = fmt.Errorf("upstream %s timeout", stage)
		stopped = true
		mu.Unlock()
		_ = closer.Close()
	}
	t1 := time.AfterFunc(first, func() { expire("first model event") })
	t2 := time.AfterFunc(generation, func() { expire("first generation") })
	var t3 *time.Timer
	note := func(s *completionState, obj map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		if len(s.choices) > 0 {
			t1.Stop()
		}
		for _, c := range s.choices {
			if c.finish != "" || len(c.tools) > 0 {
				t2.Stop()
			}
			for _, k := range []string{"content", "reasoning", "reasoning_content", "refusal"} {
				if v, ok := c.message[k].(string); ok && v != "" {
					t2.Stop()
				}
			}
		}
		if s.complete() == nil && t3 == nil {
			t3 = time.AfterFunc(tail, func() { expire("tail") })
		}
	}
	stop := func() {
		mu.Lock()
		stopped = true
		t1.Stop()
		t2.Stop()
		if t3 != nil {
			t3.Stop()
		}
		mu.Unlock()
	}
	cause := func() error { mu.Lock(); defer mu.Unlock(); return failure }
	return note, stop, cause
}

func WithResponseTimeouts(first, generation, tail time.Duration) StreamOption {
	return func(o *streamOptions) { o.firstEvent = first; o.firstGeneration = generation; o.tail = tail }
}

func consume(r io.Reader, s *completionState, emit func(sseEvent, map[string]any) error, o streamOptions) (result error) {
	note, stop, cause := watchResponse(r, o)
	defer func() {
		stop()
		if err := cause(); err != nil {
			result = err
		}
	}()
	br := bufio.NewReaderSize(r, 64*1024)
	// Native endpoints occasionally return a complete JSON response rather than SSE.
	for {
		p, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return errEmptyStream
			}
			return err
		}
		if p[0] == ' ' || p[0] == '\n' || p[0] == '\r' || p[0] == '\t' {
			_, _ = br.ReadByte()
			continue
		}
		break
	}
	first, _ := br.Peek(1)
	if first[0] == '{' || first[0] == '[' {
		raw, err := io.ReadAll(io.LimitReader(br, maxAggregate+1))
		if err != nil {
			return err
		}
		if len(raw) > maxAggregate {
			return fmt.Errorf("upstream response too large")
		}
		obj, err := jsondoc.Object(raw)
		if err != nil {
			return err
		}
		if e := errorFrame(obj, string(raw), ""); e != nil {
			if o.onErrorFrame != nil {
				o.onErrorFrame(e.Payload)
			}
			return e
		}
		if obj["object"] != "chat.completion" {
			return fmt.Errorf("unsupported upstream JSON response")
		}
		if err = s.add(obj); err != nil {
			return err
		}
		note(s, obj)
		if o.onFrame != nil {
			o.onFrame(obj)
		}
		if err = s.complete(); err != nil {
			return err
		}
		if emit != nil {
			for _, v := range obj["choices"].([]any) {
				ch := v.(map[string]any)
				ch["delta"] = ch["message"]
				delete(ch, "message")
			}
			obj["object"] = "chat.completion.chunk"
			return emit(sseEvent{}, obj)
		}
		return nil
	}
	parser := eventReader{br: br}
	type buffered struct {
		event sseEvent
		obj   map[string]any
	}
	var prelude []buffered
	for {
		e, err := parser.next()
		if err == io.EOF {
			return s.complete()
		}
		if err != nil {
			return err
		}
		if e.Data == "[DONE]" {
			return s.complete()
		}
		obj, err := jsondoc.Object([]byte(e.Data))
		if err != nil {
			return fmt.Errorf("invalid upstream event: %w", err)
		}
		if fe := errorFrame(obj, e.Data, e.Event); fe != nil {
			if o.onErrorFrame != nil {
				o.onErrorFrame(e.Data)
			}
			return fe
		}
		s.bytes += len(e.Data)
		if s.bytes > maxAggregate {
			return fmt.Errorf("completion exceeds bounded state limit")
		}
		if err = s.add(obj); err != nil {
			return err
		}
		note(s, obj)
		if o.onFrame != nil {
			o.onFrame(obj)
		}
		if emit != nil {
			if len(s.choices) == 0 {
				prelude = append(prelude, buffered{e, obj})
				continue
			}
			for _, b := range prelude {
				if err = emit(b.event, b.obj); err != nil {
					return err
				}
			}
			prelude = nil
			if err = emit(e, obj); err != nil {
				return err
			}
			// Native Chat can discard delivered text; translating consumers retain
			// bounded state for their terminal response object.
			if !s.aggregate {
				for _, c := range s.choices {
					for _, k := range []string{"content", "reasoning", "reasoning_content", "refusal", "annotations"} {
						delete(c.message, k)
						delete(c.messageStrings, k)
					}
					delete(c.fields, "logprobs")
				}
			}
		}
	}
}

// ConsumeCompletion shares the native decoder, limits, observers, deadlines and
// success predicate with Aggregate/StreamHint. emit sees validated Chat chunks
// (including JSON snapshots converted to a chunk). It must not mutate them or
// acknowledge completion before this function returns successfully.
func ConsumeCompletion(r io.Reader, emit func(map[string]any) error, opts ...StreamOption) (map[string]any, error) {
	o := streamOptions{expected: 1}
	for _, f := range opts {
		f(&o)
	}
	s := newCompletion(o.expected, true)
	s.emptyToolIdentity = o.emptyToolIdentity
	s.declaredToolNames = o.declaredToolNames
	err := consume(r, s, func(_ sseEvent, obj map[string]any) error {
		if emit == nil {
			return nil
		}
		return emit(obj)
	}, o)
	if err != nil {
		return nil, err
	}
	return s.response(), nil
}

func Aggregate(r io.Reader, opts ...StreamOption) (map[string]any, error) {
	o := streamOptions{expected: 1}
	for _, f := range opts {
		f(&o)
	}
	s := newCompletion(o.expected, true)
	if err := consume(r, s, nil, o); err != nil {
		return nil, err
	}
	return s.response(), nil
}
func Stream(w http.ResponseWriter, r io.Reader) error { return StreamHint(w, r, nil) }
func StreamHint(w http.ResponseWriter, r io.Reader, hintFn func(string) string, opts ...StreamOption) error {
	o := streamOptions{expected: 1}
	for _, f := range opts {
		f(&o)
	}
	committed := false
	write := func(e sseEvent, obj map[string]any) error {
		if !committed {
			h := w.Header()
			h.Set("Content-Type", "text/event-stream")
			h.Set("Cache-Control", "no-cache")
			h.Set("X-Accel-Buffering", "no")
			committed = true
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		var b strings.Builder
		if e.Event != "" {
			fmt.Fprintf(&b, "event: %s\n", e.Event)
		}
		if e.ID != "" {
			fmt.Fprintf(&b, "id: %s\n", e.ID)
		}
		fmt.Fprintf(&b, "data: %s\n\n", raw)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err = io.WriteString(w, b.String()); err != nil {
			return &DownstreamWriteError{err}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return nil
	}
	s := newCompletion(o.expected, false)
	err := consume(r, s, write, o)
	var we *DownstreamWriteError
	if errors.As(err, &we) {
		return err
	}
	if err != nil {
		status := 502
		obj := map[string]any{"error": map[string]any{"type": "upstream_error", "code": "upstream_protocol_error", "message": err.Error()}}
		var fe *FrameError
		if errors.As(err, &fe) {
			status = fe.Status
			obj = ErrorResponse(fe.Kind, fe.Payload)
			if hint := frameGatewayHint(hintFn, fe.Payload); hint != "" {
				obj["error"].(map[string]any)["gateway_hint"] = hint
			}
		}
		if !committed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if e := json.NewEncoder(w).Encode(obj); e != nil {
				return &DownstreamWriteError{e}
			}
			return err
		}
		if e := write(sseEvent{}, obj); e != nil {
			return e
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, e := io.WriteString(w, "data: [DONE]\n\n"); e != nil {
		return &DownstreamWriteError{e}
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return err
}
func ErrorResponse(kind ErrKind, payload string) map[string]any {
	e := map[string]any{"type": "upstream_error", "code": kind.String(), "message": "upstream rejected the request"}
	if obj, err := jsondoc.Object([]byte(payload)); err == nil {
		if inner, ok := obj["error"].(map[string]any); ok {
			obj = inner
		}
		safe := map[string]any{}
		for _, k := range []string{"code", "message", "msg", "type", "param", "requestId", "request_id"} {
			if v, ok := obj[k]; ok {
				safe[k] = v
			}
		}
		e["upstream"] = safe
		if v, ok := obj["message"].(string); ok {
			e["message"] = v
		} else if v, ok := obj["msg"].(string); ok {
			e["message"] = v
		}
	} // Non-JSON error bodies are not reflected (may contain credentials/HTML).
	return map[string]any{"error": e}
}
func frameGatewayHint(fn func(string) string, p string) (out string) {
	if fn == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()
	return strings.TrimSpace(fn(p))
}
func attachHintToErrorFrame(payload, hint string) string {
	m, err := jsondoc.Object([]byte(payload))
	if err != nil {
		return payload
	}
	e, ok := m["error"].(map[string]any)
	if !ok {
		return payload
	}
	e["gateway_hint"] = hint
	b, err := json.Marshal(m)
	if err != nil {
		return payload
	}
	return string(b)
}
