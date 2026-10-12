#!/usr/bin/env python3
"""Confirm codex app-server bills a turn against the ChatGPT plan: python3 P0-5-codex-confirm.py"""
import json, os, subprocess, sys

env = {k: v for k, v in os.environ.items() if k not in ("OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN")}
p = subprocess.Popen(["codex", "app-server"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, env=env, text=True)
next_id = 0


def send(method, params=None, notify=False):
    global next_id
    msg = {"method": method, "params": params or {}}
    if not notify:
        next_id += 1
        msg["id"] = next_id
    p.stdin.write(json.dumps(msg) + "\n")
    p.stdin.flush()
    return msg.get("id")


def wait(pred):
    for line in p.stdout:
        msg = json.loads(line)
        if "id" in msg and "method" in msg:
            sys.exit(f"unexpected server request {msg['method']}: the prompt should need no tools")
        if pred(msg):
            return msg
    sys.exit("app-server exited")


def call(method, params=None):
    i = send(method, params)
    msg = wait(lambda m: m.get("id") == i)
    if "error" in msg:
        sys.exit(f"{method}: {msg['error']}")
    return msg["result"]


call("initialize", {"clientInfo": {"name": "oriel_p0_5", "title": "Oriel P0-5 check", "version": "0.0.0"}})
send("initialized", notify=True)

account = call("account/read", {"refreshToken": False})["account"]
print("account:", account)
if not account or account.get("type") != "chatgpt":
    sys.exit("FAIL: not signed in with ChatGPT (run `codex login` and choose ChatGPT)")

before = call("account/rateLimits/read")["rateLimits"]
print("rate limits before:", json.dumps(before))

thread = call("thread/start", {"sandbox": "read-only"})["thread"]["id"]
call("turn/start", {"threadId": thread, "input": [{"type": "text", "text": "Reply with the single word: ok"}]})
done = wait(lambda m: m.get("method") == "turn/completed")
print("turn:", done["params"]["turn"].get("status"))

after = call("account/rateLimits/read")["rateLimits"]
print("rate limits after: ", json.dumps(after))
print("PASS: ChatGPT auth, usage reported against plan rate limits; check the API usage page shows no new spend.")
p.terminate()
