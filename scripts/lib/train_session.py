#!/usr/bin/env python3
"""Training sessions for a Pi executable built with the µPi runtime (no AOT yet).

The first starts the TUI, runs prompts that read files, renders markdown and code, and quits, while the runtime records which
functions run, in which order (BUN_BYTECODE_ORDER_OUT), and which regular expressions the program builds at run time
(BUN_JSC_aotRecordRegExpsPath). The order is what the executable is laid out by, so this session is the plain start and the
plain turn.

The second records regular expressions only, and is there to run as many of them as it can: an answer that uses every kind of
Markdown and a code block in every language Pi highlights (training.md), streamed in so that every partial state is rendered;
tool calls whose results the TUI renders (a file written, read with highlighting, edited into a diff, commands, errors); and
the editor's completions. A regular expression that is not recorded is not compiled into the executable, and runs interpreted.
Usage: train_session.py EXE ORDER_OUT REGEXPS_OUT"""
import os
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "bench"))
from harness import DONE, MODEL_ARGS, PROMPT, Tty, fake_model, pi_env, pi_home, workdir  # noqa: E402

exe, order, regexps = (os.path.abspath(a) for a in sys.argv[1:4])
for f in (order, regexps):
    if os.path.exists(f):
        os.remove(f)
with fake_model() as port, pi_home(port) as home, workdir() as cwd:
    env = pi_env(home, {"BUN_BYTECODE_ORDER_OUT": order, "BUN_JSC_aotRecordRegExpsPath": regexps})
    tty = Tty([exe, "--no-session", *MODEL_ARGS], env, cwd)
    deadline = time.perf_counter() + 120
    assert tty.wait_for("fake-model", 0, deadline), "the TUI did not start"
    tty.settle(0.2, deadline)
    for _ in range(3):
        start = len(tty.buf)
        tty.send(PROMPT.encode() + b"\r")
        assert tty.wait_for(DONE, start, deadline), "no answer"
        tty.settle(0.2, deadline)
    for command in (b"/hotkeys\r", b"\x1b", b"/session\r", b"\x1b", b"/model\r", b"\x1b"):
        tty.send(command)
        tty.settle(0.3, deadline)
    status, _ = tty.quit()
    assert status == 0, f"the TUI exited with {status}"

more = regexps + ".more"
if os.path.exists(more):
    os.remove(more)
with fake_model(pace_ms=1, script="fake_model_stress.py") as port, pi_home(port) as home, workdir() as cwd:
    tty = Tty([exe, "--no-session", *MODEL_ARGS], pi_env(home, {"BUN_JSC_aotRecordRegExpsPath": more}), cwd, cols=120, rows=40)
    deadline = time.perf_counter() + 600
    assert tty.wait_for("fake-model", 0, deadline), "the TUI did not start"
    tty.settle(0.2, deadline)
    training = Path(__file__).resolve().parent / "training.md"
    for scenario, done in ((f"mdfile:{training}", "Done: mdfile, answer 1."), ("train", "Done: train."), (f"mdfile:{training}", "Done: mdfile, answer 3.")):
        start = len(tty.buf)
        tty.send(f"SCENARIO {scenario} CWD {cwd}".encode() + b"\r")
        assert tty.wait_for(done, start, deadline), f"no answer to {scenario}"
        tty.settle(0.3, deadline)
    # The editor: command and file completions, a shell command, the selectors with something typed into them.
    for keys in (b"/", b"mod", b"\x1b", b"\x15", b"@fix", b"\t", b"\x1b", b"\x15", b"!ls -la fixture\r", b"/model\r", b"fake", b"\x1b",
                 b"/settings\r", b"\x1b", b"/hotkeys\r", b"\x1b", b"/session\r", b"\x1b", b"plain text with a https://example.com/link", b"\x15"):
        tty.send(keys)
        tty.settle(0.3, deadline)
    status, _ = tty.quit()
    assert status == 0, f"the TUI exited with {status} in the second session"
with open(regexps, "a") as out, open(more) as extra:
    out.write(extra.read())
os.remove(more)
print("recorded", order, regexps)
