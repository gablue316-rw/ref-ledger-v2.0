#!/usr/bin/env python3

"""
Run the Ref Ledger pytest suite and generate HTML reports.

Each run creates:

    tests/reports/test-report-YYYY-MM-DD_HH-MM-SS.html

The generated report is also copied to:

    tests/reports/latest.html

Usage:

    python run_tests.py

Run one test file:

    python run_tests.py tests/e2e/test_import_officials_pytest.py

Pass additional pytest arguments:

    python run_tests.py tests/e2e/test_import_officials_pytest.py -v -s

Run a specific test:

    python run_tests.py \
        tests/e2e/test_import_officials_pytest.py::test_import_officials
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from datetime import datetime
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parent

DEFAULT_TEST_PATH = PROJECT_ROOT / "tests" / "e2e"
REPORT_DIRECTORY = PROJECT_ROOT / "tests" / "reports"
LATEST_REPORT = REPORT_DIRECTORY / "latest.html"


def build_report_filename() -> Path:
    """Return a timestamped HTML report path."""

    timestamp = datetime.now().strftime("%Y-%m-%d_%H-%M-%S")

    return REPORT_DIRECTORY / f"test-report-{timestamp}.html"


def build_pytest_command(
    report_path: Path,
    user_arguments: list[str],
) -> list[str]:
    """Build the pytest command."""

    arguments = user_arguments.copy()

    # Run all E2E tests when no test path was supplied.
    if not arguments:
        arguments.append(str(DEFAULT_TEST_PATH))

    command = [
        sys.executable,
        "-m",
        "pytest",
        *arguments,
    ]

    # Add these options only when the user did not already supply them.
    if "-v" not in arguments and "--verbose" not in arguments:
        command.append("-v")

    if "-s" not in arguments and "--capture=no" not in arguments:
        command.append("-s")

    if not any(
        argument == "--html" or argument.startswith("--html=")
        for argument in arguments
    ):
        command.append(f"--html={report_path}")

    if "--self-contained-html" not in arguments:
        command.append("--self-contained-html")

    return command


def copy_to_latest(report_path: Path) -> None:
    """Copy the timestamped report to latest.html."""

    if not report_path.exists():
        print(
            f"\nWarning: pytest did not create the expected report:\n"
            f"  {report_path}"
        )
        return

    shutil.copy2(report_path, LATEST_REPORT)

    print("\nHTML reports:")
    print(f"  Timestamped: {report_path}")
    print(f"  Latest:      {LATEST_REPORT}")


def main() -> int:
    """Run pytest and return its exit code."""

    REPORT_DIRECTORY.mkdir(parents=True, exist_ok=True)

    report_path = build_report_filename()

    user_arguments = sys.argv[1:]

    command = build_pytest_command(
        report_path=report_path,
        user_arguments=user_arguments,
    )

    print("Running pytest:\n")
    print(" ".join(f'"{part}"' if " " in part else part for part in command))
    print()

    try:
        completed_process = subprocess.run(
            command,
            cwd=PROJECT_ROOT,
            check=False,
        )
    except KeyboardInterrupt:
        print("\nTest run interrupted.")
        return 130
    except OSError as error:
        print(f"\nUnable to start pytest: {error}")
        return 1

    copy_to_latest(report_path)

    if completed_process.returncode == 0:
        print("\nAll tests passed.")
    else:
        print(
            f"\nPytest finished with exit code "
            f"{completed_process.returncode}."
        )

    return completed_process.returncode


if __name__ == "__main__":
    raise SystemExit(main())