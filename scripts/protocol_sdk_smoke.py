"""Opt-in SDK smoke client, invoked by TestProtocolSDKSmoke against its fake gateway.

Run with openai==3.26.0 and anthropic==1.12.1 (not project runtime dependencies).
Never point this script at real accounts: test-only model IDs and credentials.
"""
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
    for model in ("text", "tools", "length"):
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
        if model == "tools":
            calls = result.output
            assert [v.call_id for v in calls] == ["a", "b"]
            assert [v.name for v in calls] == ["lookup", "lookup"]
            assert json.loads(calls[0].arguments)["n"] == 9007199254740993
            history = initial + [v.model_dump(exclude_none=True) for v in calls]
            history += [{"type": "function_call_output", "call_id": v.call_id, "output": "ok"} for v in calls]
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
        assert result.stop_reason == {"text": "end_turn", "length": "max_tokens", "tools": "tool_use"}[model]
        if model == "tools":
            calls = result.content
            assert [v.id for v in calls] == ["a", "b"]
            assert calls[0].input["n"] == 9007199254740993
            history = initial + [{"role": "assistant", "content": [v.model_dump(exclude_none=True) for v in calls]}]
            history += [{"role": "user", "content": [{"type": "tool_result", "tool_use_id": v.id, "content": "ok"} for v in calls]}]
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
print(f"SDK smoke passed: openai {openai.__version__}, anthropic {anthropic.__version__}; text/tools/two-turn/length/truncation/usage")
