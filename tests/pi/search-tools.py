"""µPi's grep and find tools in a built Pi: searching in-process (no ripgrep or fd on PATH, nothing downloaded:
PI_OFFLINE), following .gitignore, in path order; off by default (their schemas are there to turn on with --tools), on with it,
and not on with FFF's extension loaded.
  python3 tests/pi/search-tools.py PI
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

pi = str(Path(sys.argv[1]).resolve())
failures = 0
STEPS = [("grep", {"pattern": "needle", "path": "repo"}), ("find", {"pattern": "*.ts", "path": "repo"})]
seen = {"tools": [], "results": []}


def check(name, ok, detail=""):
    global failures
    print(f"{'PASS' if ok else 'FAIL'} {name}{'' if ok else f': {detail}'}")
    failures += 0 if ok else 1


def chunk(delta=None, finish=None):
    return b"data: " + json.dumps({"id": "f", "object": "chat.completion.chunk", "model": "fake-model",
                                   "choices": [{"index": 0, "delta": delta or {}, "finish_reason": finish}]}).encode() + b"\n\n"


class Model(BaseHTTPRequestHandler):
    """Offers one scripted tool call per turn, if the tool was offered; records the tools offered and the results."""

    def log_message(self, *args):
        pass

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        offered = [t.get("function", {}).get("name") for t in req.get("tools", [])]
        results = [m.get("content") for m in req.get("messages", []) if m.get("role") == "tool"]
        seen["tools"], seen["results"] = offered, results
        steps = [step for step in STEPS if step[0] in offered]
        out = [chunk({"role": "assistant", "content": ""})]
        if len(results) < len(steps):
            name, args = steps[len(results)]
            out.append(chunk({"tool_calls": [{"index": 0, "id": f"c{len(results)}", "type": "function",
                                               "function": {"name": name, "arguments": json.dumps(args)}}]}))
            out.append(chunk(finish="tool_calls"))
        else:
            out += [chunk({"content": "done"}), chunk(finish="stop")]
        body = b"".join(out) + b"data: [DONE]\n\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


server = ThreadingHTTPServer(("127.0.0.1", 0), Model)
threading.Thread(target=server.serve_forever, daemon=True).start()
tmp = Path(tempfile.mkdtemp(prefix="pibolt-search-"))
try:
    agent, work = tmp / "agent", tmp / "work"
    agent.mkdir()
    (agent / "models.json").write_text(json.dumps({"providers": {"fake": {
        "baseUrl": f"http://127.0.0.1:{server.server_address[1]}/v1", "api": "openai-completions", "apiKey": "fake",
        "models": [{"id": "fake-model", "contextWindow": 200000, "maxTokens": 8192}]}}}))
    repo = work / "repo"
    for rel, text in {"b.ts": "needle b\n", "a/c.ts": "needle c\n", "build/out.ts": "needle built\n", "notes.md": "no\n"}.items():
        (repo / rel).parent.mkdir(parents=True, exist_ok=True)
        (repo / rel).write_bytes(text.encode())  # (Not write_text: on Windows it would write CRLF.)
    (repo / ".git").mkdir()
    (repo / ".gitignore").write_bytes(b"build/\n")
    # (Not FFF itself: a stand-in at the path FFF's package is loaded from.)
    fff = tmp / "npm" / "node_modules" / "opensec-pi-fff"
    fff.mkdir(parents=True)
    (fff / "index.ts").write_text("export default function () {}\n")

    def session(settings, *args):
        (agent / "settings.json").write_text(json.dumps({"lastChangelogVersion": "9999.0.0", **settings}))
        env = {"HOME": str(tmp), "PATH": "/usr/bin:/bin", "PI_CODING_AGENT_DIR": str(agent), "PI_OFFLINE": "1",
               "PI_SKIP_VERSION_CHECK": "1", "PI_TELEMETRY": "0", "TERM": "dumb"}
        if os.name == "nt":
            # (What a Windows program needs to start, as tests/pi/run.ps1 gives it; still no ripgrep or fd on PATH.)
            root = os.environ.get("SystemRoot", r"C:\Windows")
            env.update({"SystemRoot": root, "windir": root, "PATH": rf"{root}\System32;{root}", "USERPROFILE": str(tmp),
                        "TEMP": os.environ.get("TEMP", ""), "TMP": os.environ.get("TMP", ""),
                        "ComSpec": os.environ.get("ComSpec", ""), "PATHEXT": os.environ.get("PATHEXT", "")})
        r = subprocess.run([pi, "-p", "--no-session", "--model", "fake/fake-model", *args, "go"], cwd=work, env=env,
                           capture_output=True, text=True, encoding="utf-8", errors="replace", stdin=subprocess.DEVNULL,
                           timeout=120)
        return r, list(seen["tools"]), list(seen["results"])

    r, tools, results = session({})
    check("grep and find are not on by default (settings-manager: their schemas are off until turned on)",
          r.returncode == 0 and "grep" not in tools and "find" not in tools, f"exit {r.returncode}, tools {tools}")
    r, tools, results = session({}, "--tools", "grep,find")
    check("grep searches in-process, leaving out what .gitignore does, in path order",
          len(results) == 2 and results[0] == "a/c.ts:1: needle c\nb.ts:1: needle b", repr(results[:1]))
    check("find too", len(results) == 2 and results[1] == "a/c.ts\nb.ts", repr(results[1:]))
    check("nothing was downloaded", not (agent / "bin").exists() or not any((agent / "bin").iterdir()))

    r, tools, _ = session({"extensions": [str(fff / "index.ts")]})
    check("with FFF loaded they are not on by default", r.returncode == 0 and "grep" not in tools and "find" not in tools,
          f"tools {tools}, {r.stderr[-300:]}")
    r, tools, results = session({"extensions": [str(fff / "index.ts")]}, "--tools", "read,grep")
    check("--tools grep still turns grep on", "grep" in tools and len(results) == 1, f"tools {tools}")
finally:
    server.shutdown()
    shutil.rmtree(tmp, ignore_errors=True)

print("search tools:", "all passed" if failures == 0 else f"{failures} failed")
sys.exit(1 if failures else 0)
