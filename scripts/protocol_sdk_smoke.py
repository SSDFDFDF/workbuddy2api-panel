"""Opt-in SDK smoke client, invoked by TestProtocolSDKSmoke against its fake gateway.

Run with openai==3.26.0 and anthropic==1.12.1 (not project runtime dependencies).
Never point this script at real accounts: test-only model IDs and credentials.
"""
import base64
import json
import sys

import anthropic
import openai

base = sys.argv[1]
assert base.startswith("http://127.0.0.1:"), "only the loopback test gateway is allowed"
o = openai.OpenAI(
    base_url=base + "/v1", api_key="sdk-test-key", max_retries=0,
    http_client=openai.DefaultHttpxClient(trust_env=False, timeout=15),
)
a = anthropic.Anthropic(
    base_url=base, api_key="sdk-test-key", max_retries=0,
    http_client=anthropic.DefaultHttpxClient(trust_env=False, timeout=15),
)


def check_usage(value, is_anthropic=False):
    u = value.usage
    assert u.input_tokens == (11 if is_anthropic else 20), u
    assert u.output_tokens == 3, u
    if is_anthropic:
        assert u.cache_read_input_tokens == 7, u
        assert u.cache_creation_input_tokens == 2, u
    else:
        assert u.total_tokens == 23, u
        assert u.input_tokens_details.cached_tokens == 7, u
        assert u.input_tokens_details.cache_write_tokens == 2, u


schema = {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}
otools = [{"type": "function", "name": "lookup", "parameters": schema, "strict": False}]
atools = [{"name": "lookup", "input_schema": schema}]
initial = [{"role": "user", "content": "hi"}]

for stream in (False, True):
    for model in ("text", "tools", "tools-empty", "tools-repeated", "tools-cumulative", "length"):
        is_tools = model.startswith("tools")
        params = dict(model="cn:" + model, store=False, input=initial, tools=otools)
        if stream:
            with o.responses.stream(**params) as s:
                streamed = list(s)
                if model == "length":
                    # This SDK's helper only finalizes response.completed;
                    # consume the real incomplete event, never fake success.
                    result = next(e.response for e in streamed if e.type == "response.incomplete")
                    assert not any(e.type == "response.completed" for e in streamed)
                else:
                    result = s.get_final_response()
        else:
            result = o.responses.create(**params)
        check_usage(result)
        assert result.status == ("incomplete" if model == "length" else "completed")
        if is_tools:
            calls = result.output
            assert [v.call_id for v in calls] == ["a", "b"]
            assert [v.name for v in calls] == ["lookup", "lookup"]
            assert json.loads(calls[0].arguments)["n"] == 9007199254740993
            history = initial + [v.model_dump(exclude_none=True) for v in calls]
            history += [{"type": "function_call_output", "call_id": v.call_id, "output": [] if model == "tools-empty" else "ok"} for v in calls]
            follow = dict(params, input=history)
            if stream:
                with o.responses.stream(**follow) as s:
                    list(s)
                    final = s.get_final_response()
            else:
                final = o.responses.create(**follow)
            assert final.output_text == "工具完成", final
        else:
            assert result.output_text == "你好", result
            if model == "length":
                assert result.incomplete_details.reason == "max_output_tokens"

        params = dict(model="cn:" + model, max_tokens=100, messages=initial, tools=atools)
        if stream:
            with a.messages.stream(**params) as s:
                list(s)
                result = s.get_final_message()
        else:
            result = a.messages.create(**params)
        check_usage(result, True)
        assert result.stop_reason == ("tool_use" if is_tools else {"text": "end_turn", "length": "max_tokens"}[model])
        if is_tools:
            calls = result.content
            assert [v.id for v in calls] == ["a", "b"]
            assert calls[0].input["n"] == 9007199254740993
            history = initial + [{"role": "assistant", "content": [v.model_dump(exclude_none=True) for v in calls]}]
            history += [{"role": "user", "content": [{"type": "tool_result", "tool_use_id": v.id, "content": [] if model == "tools-empty" else "ok"} for v in calls]}]
            follow = dict(params, messages=history)
            if stream:
                with a.messages.stream(**follow) as s:
                    list(s)
                    final = s.get_final_message()
            else:
                final = a.messages.create(**follow)
            assert final.content[0].text == "工具完成", final
        else:
            assert result.content[0].text == "你好", result

# Image parts: the official SDK's field shapes must map onto the upstream's only
# media form (object image_url with an inline data URL), and tool-result images
# must be hoisted into a user message after the tool batch by default.
png_b64 = base64.b64encode(b"\x89PNG\r\n\x1a\n").decode()
data_url = "data:image/png;base64," + png_b64
image_input = [{"role": "user", "content": [
    {"type": "input_image", "image_url": data_url},
    {"type": "input_text", "text": "看图"},
]}]
for stream in (False, True):
    params = dict(model="cn:images", store=False, input=image_input)
    if stream:
        with o.responses.stream(**params) as s:
            list(s)
            result = s.get_final_response()
    else:
        result = o.responses.create(**params)
    check_usage(result)
    assert result.output_text == "你好", result
image_messages = [{"role": "user", "content": [
    {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": png_b64}},
    {"type": "text", "text": "看图"},
]}]
for stream in (False, True):
    params = dict(model="cn:images", max_tokens=100, messages=image_messages)
    if stream:
        with a.messages.stream(**params) as s:
            list(s)
            result = s.get_final_message()
    else:
        result = a.messages.create(**params)
    check_usage(result, True)
    assert result.content[0].text == "你好", result
tool_image_messages = [
    {"role": "assistant", "content": [
        {"type": "tool_use", "id": "shot", "name": "shot", "input": {"n": 9007199254740993}},
    ]},
    {"role": "user", "content": [
        {"type": "tool_result", "tool_use_id": "shot", "content": [
            {"type": "text", "text": "captured"},
            {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": png_b64}},
        ]},
    ]},
]
for stream in (False, True):
    params = dict(model="cn:images-tool", max_tokens=100, messages=tool_image_messages)
    if stream:
        with a.messages.stream(**params) as s:
            list(s)
            result = s.get_final_message()
    else:
        result = a.messages.create(**params)
    assert result.content[0].text == "工具完成", result

# Tool-choice constraints are part of the translated contract, not hints that
# a completed upstream response may silently violate.
for params in (
    dict(model="cn:text", tool_choice="required"),
    dict(model="cn:tools", tool_choice="none"),
    dict(model="cn:tools", parallel_tool_calls=False),
):
    p = dict(store=False, input=initial, tools=otools, **params)
    try:
        o.responses.create(**p)
    except openai.APIStatusError as e:
        assert e.status_code == 502
    else:
        raise AssertionError("Responses accepted a violated tool-choice constraint")
    try:
        with o.responses.stream(**p) as s:
            events = list(s)
        assert any(e.type == "response.failed" for e in events)
        assert not any(e.type.startswith("response.function_call") for e in events)
    except openai.APIStatusError as e:
        assert e.status_code == 502  # tool-only failure before starting SSE

for model, choice in (("text", {"type": "any"}), ("tools", {"type": "none"}),
                      ("tools", {"type": "auto", "disable_parallel_tool_use": True})):
    p = dict(model="cn:" + model, max_tokens=100, messages=initial, tools=atools, tool_choice=choice)
    try:
        a.messages.create(**p)
    except anthropic.APIStatusError as e:
        assert e.status_code == 502
    else:
        raise AssertionError("Messages accepted a violated tool-choice constraint")

# Namespaces stay stateless: same local function in two groups, repeated wire
# names, stable identity and text-block tool results across a fresh second turn.
ntools = [
    {"type": "namespace", "name": ns, "description": "", "tools": [
        dict(otools[0], description="keep child description")
    ]}
    for ns in ("crm", "fs")
]
for stream, namespace_ids in ((stream, ids) for stream in (False, True)
                              for ids in (("crm", "fs"), ("c" * 64, "d" * 64))):
    tools = [dict(tool, name=ns) for tool, ns in zip(ntools, namespace_ids)]
    model = "namespace-long" if len(namespace_ids[0]) == 64 else "namespace"
    params = dict(model="cn:" + model, store=False, input=initial, tools=tools)
    if stream:
        with o.responses.stream(**params) as s:
            events = list(s)
            result = s.get_final_response()
        announced = [e.item for e in events if e.type == "response.output_item.added"]
        assert [c.namespace for c in announced] == list(namespace_ids)
    else:
        result = o.responses.create(**params)
    assert result.tools[0].type == "namespace"
    calls = result.output
    assert [c.name for c in calls] == ["lookup", "lookup"]
    assert [c.namespace for c in calls] == list(namespace_ids)
    assert [c.call_id for c in calls] == ["a", "b"]
    check_usage(result)
    history = initial + [c.model_dump(exclude_none=True) for c in calls]
    history += [
        {"type": "function_call_output", "call_id": c.call_id, "output": [
            {"type": "input_text", "text": " A\n"}, {"type": "input_text", "text": "B "}
        ]}
        for c in calls
    ]
    if stream:
        with o.responses.stream(**dict(params, input=history)) as s:
            list(s)
            result = s.get_final_response()
    else:
        result = o.responses.create(**dict(params, input=history))
    assert result.output_text == "工具完成"
    # Retire both tools in a fresh request. Explicit historical namespaces still
    # map deterministically, while no declaration or permission is synthesized.
    retired_history = initial + [c.model_dump(exclude_none=True) for c in calls]
    retired_history += [
        {"type": "function_call_output", "call_id": c.call_id, "output": []}
        for c in calls
    ]
    retired = dict(model="cn:" + model + "-retired", store=False,
                   input=retired_history, tool_choice="none")
    if stream:
        with o.responses.stream(**retired) as s:
            list(s)
            result = s.get_final_response()
    else:
        result = o.responses.create(**retired)
    assert result.output_text == "工具完成"
    check_usage(result)

with o.responses.stream(model="cn:namespace-cutoff", store=False, input=initial, tools=ntools) as s:
    events = list(s)
    result = next(e.response for e in events if e.type == "response.incomplete")
    assert [c.namespace for c in result.output] == ["crm", "fs"]
    assert all(c.status == "incomplete" for c in result.output)
    assert result.output[0].arguments == '{"n":'
    assert not any(e.type.startswith("response.function_call") for e in events)
    assert not any(e.type == "response.output_item.added" for e in events)

# EasyInputMessage assistant history and output replay are both text inputs.
for typ in ("input_text", "output_text"):
    history = initial + [
        {"role": "assistant", "content": [{"type": typ, "text": "之前的回答"}]},
        {"role": "user", "content": "continue"},
    ]
    assert o.responses.create(model="cn:text", store=False, input=history).output_text == "你好"
    with o.responses.stream(model="cn:text", store=False, input=history) as s:
        list(s)
        assert s.get_final_response().output_text == "你好"

# Partial tools are diagnostic data only: no call/arguments lifecycle events.
# An event-driven tool runner must never be invited to execute them.
for model, reason in (("partial-tool", "max_output_tokens"), ("filtered-tool", "content_filter")):
    params = dict(model="cn:" + model, store=False, input=initial, tools=otools)
    results = [o.responses.create(**params)]
    with o.responses.stream(**params) as s:
        streamed = list(s)
        assert not any(e.type.startswith("response.function_call") for e in streamed)
        assert not any(e.type == "response.completed" for e in streamed)
        assert not any(getattr(getattr(e, "item", None), "type", None) == "function_call" for e in streamed)
        results.append(next(e.response for e in streamed if e.type == "response.incomplete"))
    for result in results:
        check_usage(result)
        assert result.status == "incomplete"
        assert result.incomplete_details.reason == reason
        call = result.output[1]
        assert call.type == "function_call" and call.status == "incomplete"
        assert call.call_id == "cut" and call.name == "lookup" and call.arguments == '{"n":'
    # Anthropic cannot represent this as a finished tool input object.
    try:
        a.messages.create(model="cn:" + model, max_tokens=100, messages=initial, tools=atools)
    except anthropic.APIStatusError as e:
        assert e.status_code == 502
    else:
        raise AssertionError("Anthropic accepted partial tool input")
    try:
        with a.messages.stream(model="cn:" + model, max_tokens=100, messages=initial, tools=atools) as s:
            list(s)
    except anthropic.APIError:
        pass
    else:
        raise AssertionError("Anthropic streamed partial tool input")

with o.responses.stream(model="cn:bad-tool", store=False, input=initial, tools=otools) as s:
    streamed = list(s)
    assert any(e.type == "response.failed" for e in streamed)
    assert not any(e.type in ("response.incomplete", "response.completed") for e in streamed)

with o.responses.stream(model="cn:truncated", store=False, input="hi") as s:
    events = list(s)
    assert any(e.type == "response.failed" for e in events)
    assert not any(e.type == "response.completed" for e in events)
try:
    with a.messages.stream(model="cn:truncated", max_tokens=100, messages=initial) as s:
        list(s)
except anthropic.APIError:
    pass
else:
    raise AssertionError("Anthropic silently accepted truncated upstream")

for client, params in (
    (o.responses, dict(model="cn:truncated", store=False, input="hi")),
    (a.messages, dict(model="cn:truncated", max_tokens=100, messages=initial)),
):
    try:
        client.create(**params)
    except (openai.APIStatusError, anthropic.APIStatusError) as e:
        assert e.status_code == 502, e
    else:
        raise AssertionError("non-stream request silently accepted truncation")

o.close()
a.close()
print(f"SDK smoke passed: openai {openai.__version__}, anthropic {anthropic.__version__}; text/history/tools/name-dialects/namespaces/compact-aliases/retired-history/empty-results/text-results/images/tool-image-hoist/tool-choice/two-turn/length/partial-tools/truncation/usage")
