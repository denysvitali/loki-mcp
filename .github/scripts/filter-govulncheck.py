#!/usr/bin/env python3
"""Fail govulncheck unless all findings are explicitly allowed."""

import json
import sys
from collections import Counter
from pathlib import Path


ALLOWED_FINDINGS = {
    "GO-2026-5662": (
        "Prometheus stored-XSS advisory is currently reported with no fixed "
        "version and is reachable through the Loki SDK dependency graph."
    ),
    "GO-2026-5932": (
        "golang.org/x/crypto/openpgp is unmaintained with no fixed version. "
        "It is pulled in transitively and is not called by this module."
    ),
}


def iter_json_objects(text):
    decoder = json.JSONDecoder()
    idx = 0

    while idx < len(text):
        while idx < len(text) and text[idx].isspace():
            idx += 1

        if idx >= len(text):
            return

        obj, idx = decoder.raw_decode(text, idx)
        yield obj


def main():
    if len(sys.argv) != 2:
        print("usage: filter-govulncheck.py <govulncheck-json-output>", file=sys.stderr)
        return 2

    report_path = Path(sys.argv[1])
    try:
        objects = list(iter_json_objects(report_path.read_text()))
    except (OSError, json.JSONDecodeError) as err:
        print(f"failed to read govulncheck report: {err}", file=sys.stderr)
        return 2

    findings = Counter(
        obj["finding"]["osv"]
        for obj in objects
        if isinstance(obj, dict) and "finding" in obj and "osv" in obj["finding"]
    )

    disallowed = {osv: count for osv, count in findings.items() if osv not in ALLOWED_FINDINGS}
    if disallowed:
        print("govulncheck found unallowed vulnerabilities:", file=sys.stderr)
        for osv, count in sorted(disallowed.items()):
            print(f"- {osv}: {count} trace(s)", file=sys.stderr)
        return 1

    if not findings:
        print("govulncheck found no vulnerabilities.")
        return 0

    print("govulncheck found only allowed vulnerabilities:")
    for osv, count in sorted(findings.items()):
        print(f"- {osv}: {count} trace(s) allowed. {ALLOWED_FINDINGS[osv]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
