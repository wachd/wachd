#!/usr/bin/env python3
"""
Reads `go test -json` output from stdin and renders a readable ASCII table.

Usage:
    go test -json -tags integration -count=1 -run TestE2E ./cmd/server/ | python3 scripts/test-table.py
"""

import json
import sys

# ANSI colours
GREEN  = "\033[32m"
RED    = "\033[31m"
YELLOW = "\033[33m"
BOLD   = "\033[1m"
RESET  = "\033[0m"


def strip_prefix(name: str) -> str:
    """Remove the top-level test function prefix (TestE2E_) for brevity."""
    if name.startswith("TestE2E_"):
        return name[len("TestE2E_"):]
    if name.startswith("TestE2E/"):
        return name[len("TestE2E/"):]
    return name


def main():
    # Collect events keyed by test name
    results: dict[str, dict] = {}        # name -> {status, elapsed, parent, output}
    order: list[str] = []                # insertion order
    package_elapsed: float = 0.0
    build_error_lines: list[str] = []
    in_build_error = False

    for raw in sys.stdin:
        raw = raw.rstrip("\n")
        if not raw:
            continue

        # Pass through non-JSON lines (e.g. log output before tests start)
        try:
            ev = json.loads(raw)
        except json.JSONDecodeError:
            # Raw stderr from the test binary
            if "FAIL" in raw or "build failed" in raw.lower():
                build_error_lines.append(raw)
            continue

        action = ev.get("Action", "")
        test   = ev.get("Test", "")
        pkg    = ev.get("Package", "")

        if action == "build-fail":
            build_error_lines.append(ev.get("Output", "build failed"))
            continue

        if not test:
            # Package-level event
            if action == "pass" and "Elapsed" in ev:
                package_elapsed = ev["Elapsed"]
            if action == "fail":
                package_elapsed = ev.get("Elapsed", 0.0)
            continue

        if test not in results:
            results[test] = {"status": "run", "elapsed": 0.0, "output": []}
            order.append(test)

        if action == "output":
            line = ev.get("Output", "").rstrip("\n")
            # Only keep lines that look like user log output (not === RUN / --- PASS etc.)
            if not (line.startswith("=== RUN") or
                    line.startswith("=== PAUSE") or
                    line.startswith("=== CONT") or
                    line.startswith("--- PASS") or
                    line.startswith("--- FAIL") or
                    line.startswith("--- SKIP") or
                    line.strip() == "" or
                    line.startswith("PASS") or
                    line.startswith("FAIL") or
                    line.startswith("ok ")):
                results[test]["output"].append(line)
        elif action in ("pass", "fail", "skip"):
            results[test]["status"]  = action
            results[test]["elapsed"] = ev.get("Elapsed", 0.0)

    if build_error_lines:
        print(f"{RED}{BOLD}Build failed:{RESET}")
        for l in build_error_lines:
            print(f"  {l}")
        sys.exit(1)

    if not results:
        print(f"{YELLOW}No test results found.{RESET}")
        sys.exit(0)

    # ---- Separate top-level tests from sub-tests ----------------------------
    top_level  = [n for n in order if "/" not in n]
    sub_tests  = [n for n in order if "/" in n]

    def parent_of(name: str) -> str:
        return name.rsplit("/", 1)[0]

    def short_sub(name: str) -> str:
        """Return indented sub-test label."""
        parts = name.split("/")
        label = parts[-1].replace("_", " ")
        depth = len(parts) - 1
        indent = "    " * depth
        return f"{indent}└─ {label}"

    # ---- Build display rows -------------------------------------------------
    # Each row: (label, status, elapsed_str, is_sub)
    rows = []
    for top in top_level:
        r = results[top]
        rows.append((strip_prefix(top), r["status"], r["elapsed"], False))
        # attach sub-tests that belong to this top-level test
        for sub in sub_tests:
            if parent_of(sub) == top or parent_of(sub).startswith(top + "/"):
                rs = results[sub]
                depth = sub.count("/")
                short = "    " * depth + "└─ " + sub.split("/")[-1].replace("_", " ")
                rows.append((short, rs["status"], rs["elapsed"], True))

    # ---- Column widths ------------------------------------------------------
    col1_min = 45
    col1_w = max(col1_min, max(len(r[0]) for r in rows) + 2)
    col2_w = 8   # " ✓ PASS "
    col3_w = 9   # " 0.000s  "

    def fmt_status(s: str) -> str:
        if s == "pass":
            return f"{GREEN}✓ PASS{RESET}"
        if s == "fail":
            return f"{RED}✗ FAIL{RESET}"
        if s == "skip":
            return f"{YELLOW}⊘ SKIP{RESET}"
        return s

    def fmt_time(t: float) -> str:
        if t < 0.001:
            return "  —    "
        return f"{t:.3f}s"

    # ---- Draw table ---------------------------------------------------------
    h_line  = "─" * col1_w
    h_line2 = "─" * col2_w
    h_line3 = "─" * col3_w

    top_border    = f"┌{h_line}┬{h_line2}┬{h_line3}┐"
    header_sep    = f"├{h_line}┼{h_line2}┼{h_line3}┤"
    row_sep       = f"├{h_line}┼{h_line2}┼{h_line3}┤"
    bottom_border = f"└{h_line}┴{h_line2}┴{h_line3}┘"

    def row_line(label: str, status_str: str, time_str: str) -> str:
        # Pad label — but ANSI codes add invisible chars, so pad before colouring
        label_pad = label.ljust(col1_w - 2)
        # status_str and time_str may contain ANSI; measure visible length
        visible_status = status_str.replace(GREEN,"").replace(RED,"").replace(YELLOW,"").replace(BOLD,"").replace(RESET,"")
        visible_time   = time_str
        status_pad = status_str + " " * (col2_w - len(visible_status))
        time_pad   = " " + time_str.rjust(col3_w - 1)
        return f"│ {label_pad} │{status_pad}│{time_pad}│"

    print()
    print(top_border)
    print(row_line(f"{BOLD}Test{RESET}", f"{BOLD}Status{RESET}", f"{BOLD}Time{RESET}"))
    print(header_sep)

    passed = 0
    failed = 0
    for i, (label, status, elapsed, is_sub) in enumerate(rows):
        if not is_sub and i > 0:
            print(row_sep)
        status_str = fmt_status(status)
        time_str   = fmt_time(elapsed)
        print(row_line(label, status_str, time_str))
        if status == "pass": passed += 1
        elif status == "fail": failed += 1

    # ---- Summary row --------------------------------------------------------
    print(header_sep)
    total_top = sum(1 for r in rows if not r[3])
    if failed == 0:
        summary_label = f"{GREEN}{BOLD}{passed} passed{RESET}  ·  {failed} failed"
    else:
        summary_label = f"{GREEN}{passed} passed{RESET}  ·  {RED}{BOLD}{failed} failed{RESET}"
    print(row_line(summary_label, "", fmt_time(package_elapsed)))
    print(bottom_border)
    print()

    if failed > 0:
        # Print captured output for failed tests
        print(f"{RED}{BOLD}Failed test output:{RESET}")
        for name, r in results.items():
            if r["status"] == "fail" and r["output"]:
                print(f"\n  {BOLD}{strip_prefix(name)}{RESET}")
                for line in r["output"]:
                    print(f"    {line}")
        print()
        sys.exit(1)


if __name__ == "__main__":
    main()
