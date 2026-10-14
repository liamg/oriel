"""Oriel's pytest reporter: one JSON object per line, after a marker, on the stdout pytest started with.

Loaded with `-p oriel_pytest`. Standard library only.
"""

import json
import os
import time

MARKER = b"@@oriel-event "

# Duplicated before pytest's capture starts, so events reach the real stdout even while a test's output is captured.
_out = os.dup(1)
_phases = {}


def _emit(obj):
    obj["time"] = time.time()
    os.write(_out, b"\n" + MARKER + json.dumps(obj, separators=(",", ":")).encode() + b"\n")


def _crash(report):
    crash = getattr(report.longrepr, "reprcrash", None)
    if crash is None:
        return None
    return {"path": str(crash.path), "lineno": crash.lineno, "message": crash.message}


def pytest_sessionstart(session):
    _emit({"event": "sessionstart", "rootdir": str(session.config.rootpath)})


def pytest_collectreport(report):
    if report.failed:
        _emit({"event": "collecterror", "nodeid": report.nodeid, "longrepr": report.longreprtext, "crash": _crash(report)})


def pytest_runtest_logstart(nodeid, location):
    _phases[nodeid] = []
    _emit({"event": "start", "nodeid": nodeid, "location": list(location)})


def pytest_runtest_logreport(report):
    _phases.setdefault(report.nodeid, []).append(report)


def pytest_runtest_logfinish(nodeid, location):
    reports = _phases.pop(nodeid, [])
    outcome, when, failed, duration = "passed", "call", None, 0.0
    for r in reports:
        duration += r.duration
        if r.failed and failed is None:
            failed = r
        elif r.skipped and outcome == "passed":
            outcome, when = "skipped", r.when
    result = {"event": "result", "nodeid": nodeid, "duration": duration, "outcome": outcome, "when": when}
    if failed is not None:
        result.update(outcome="failed", when=failed.when, longrepr=failed.longreprtext, crash=_crash(failed))
    last = reports[-1] if reports else None
    if outcome == "skipped" and failed is None:
        for r in reports:
            if r.skipped and isinstance(r.longrepr, tuple) and len(r.longrepr) == 3:
                result["message"] = r.longrepr[2]
    for r in reports:
        if hasattr(r, "wasxfail"):
            result["xfail"] = r.wasxfail or True
    if last is not None:
        result["sections"] = [[name, content] for name, content in last.sections]
    _emit(result)


def pytest_sessionfinish(session, exitstatus):
    _emit({"event": "sessionfinish", "exitstatus": int(exitstatus)})
