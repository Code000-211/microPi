"""µPi's project_search in a built Pi: the docsearch extension compiled in as a built-in offers it when nothing is
installed, and it finds a section in a Markdown doc, with its path and lines; `-builtin:docsearch` in the settings turns it
off, and the tools beside it are the same as before.
  python3 tests/pi/docsearch.py PI
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

STEPS = [("project_search", {"query": "needle", "k": 10})]


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
        self.server.seen = (offered, results)
        out = [chunk({"role": "assistant", "content": ""})]
        if not results and "project_search" in offered:
            name, args = STEPS[0]
            out.append(chunk({"tool_calls": [{"index": 0, "id": "c0", "type": "function",
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


def run_checks(pi, tag, report):
    """The checks against one executable; the caller's `report` prints them. How many failed."""
    failed = []

    def check(name, ok, detail=""):
        report(name, ok, detail)
        if not ok:
            failed.append(name)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Model)
    server.seen = ([], [])
    threading.Thread(target=server.serve_forever, daemon=True).start()
    tmp = Path(tempfile.mkdtemp(prefix="pibolt-docsearch-"))
    try:
        agent, work = tmp / "agent", tmp / "work"
        agent.mkdir()
        (agent / "models.json").write_text(json.dumps({"providers": {"fake": {
            "baseUrl": f"http://127.0.0.1:{server.server_address[1]}/v1", "api": "openai-completions", "apiKey": "fake",
            "models": [{"id": "fake-model", "contextWindow": 200000, "maxTokens": 8192}]}}}))
        repo = work / "repo"
        for rel, text in {"docs/spec.md": "# Needle section\n\nThe needle lives here.\n",
                          "src/lib.ts": "export function needle() {}\n", "notes.md": "no\n"}.items():
            (repo / rel).parent.mkdir(parents=True, exist_ok=True)
            (repo / rel).write_bytes(text.encode())  # (Not write_text: on Windows it would write CRLF.)
        (repo / ".git").mkdir()
        # More files than DocIndex's small-project cutoff: with fewer, the tool is left out of the active tools.
        filler = repo / "src" / "gen"
        filler.mkdir(parents=True)
        for i in range(200):
            (filler / f"mod{i}.ts").write_bytes(b"export const fill = no;\n")

        def session(settings):
            (agent / "settings.json").write_text(json.dumps({"lastChangelogVersion": "9999.0.0", **settings}))
            env = {"HOME": str(tmp), "PATH": "/usr/bin:/bin", "PI_CODING_AGENT_DIR": str(agent), "PI_OFFLINE": "1",
                   "PI_SKIP_VERSION_CHECK": "1", "PI_TELEMETRY": "0", "TERM": "dumb"}
            if os.name == "nt":
                root = os.environ.get("SystemRoot", r"C:\Windows")
                env.update({"SystemRoot": root, "windir": root, "PATH": rf"{root}\System32;{root}",
                            "USERPROFILE": str(tmp), "TEMP": os.environ.get("TEMP", ""), "TMP": os.environ.get("TMP", ""),
                            "ComSpec": os.environ.get("ComSpec", ""), "PATHEXT": os.environ.get("PATHEXT", "")})
            r = subprocess.run([pi, "-p", "--no-session", "--model", "fake/fake-model", "go"], cwd=work, env=env,
                               capture_output=True, text=True, encoding="utf-8", errors="replace",
                               stdin=subprocess.DEVNULL, timeout=120)
            return r, server.seen[0], server.seen[1]

        r, tools, results = session({"extensions": []})
        check(f"{tag}project_search is offered with nothing installed",
              r.returncode == 0 and "project_search" in tools,
              f"exit {r.returncode}, tools {tools}, {r.stderr[-400:] or r.stdout[-400:]}")
        known = list(tools)
        check(f"{tag}project_search finds a section of the docs, with its path and lines",
              len(results) == 1 and "docs/spec.md" in results[0] and "Needle section" in results[0],
              repr(results[:1]))
        r, tools, _ = session({"extensions": ["-builtin:docsearch"]})
        check(f"{tag}-builtin:docsearch turns project_search off, and the rest of the tools stays",
              r.returncode == 0 and "project_search" not in tools and tools == [t for t in known if t != "project_search"],
              f"exit {r.returncode}, tools {tools}, {r.stderr[-400:]}")
        return len(failed)
    finally:
        server.shutdown()
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    pi = str(Path(sys.argv[1]).resolve())
    failures = 0

    def check(name, ok, detail=""):
        global failures
        print(f"{'PASS' if ok else 'FAIL'} {name}{'' if ok else f': {detail}'}")
        failures += 0 if ok else 1

    run_checks(pi, "", check)
    print("docsearch:", "all passed" if failures == 0 else f"{failures} failed")
    sys.exit(1 if failures else 0)
