import json
import os
from pathlib import Path
from typing import Any

import pytest
from playwright.sync_api import (
    Page,
    TimeoutError as PlaywrightTimeoutError,
    sync_playwright,
)
from pymongo import MongoClient


###############################################################
#                                                             #
# Test Setup: Before executing this script                    #
#                                                             #
#    1.  Set MONGODB_NAME environment variable to             #
#        refLedger_v2_test                                    #
#    2.  Set TENANT_ID environment variable to tenant id      #
#        of test user                                         #
#    3.  Restart Ref Ledger                                   #
#        (kubectl rollout restart deployment/ref-ledger)      #
#    4.  Delete all documents from officials collection       #
#        in the refLedger_v2_test database                    #
#                                                             #
###############################################################


BASE_URL = os.getenv(
    "REF_LEDGER_BASE_URL",
    "https://ref-ledger.com",
).rstrip("/")

USERNAME = os.getenv("REF_LEDGER_TEST_USERNAME")
PASSWORD = os.getenv("REF_LEDGER_TEST_PASSWORD")

TENANT_ID = os.getenv(
    "REF_LEDGER_TEST_TENANT_ID"
)

MONGODB_URI = os.getenv(
    "MONGODB_URI",
    "mongodb://localhost:27017/?replicaSet=refLedgerRS",
)

DATABASE_NAME = os.getenv(
    "REF_LEDGER_TEST_DATABASE",
    "refLedger_v2",
)


SCRIPT_DIRECTORY = Path(__file__).resolve().parent
REPORT_DIRECTORY = SCRIPT_DIRECTORY / "reports"

officials_file_value = os.getenv(
    "REF_LEDGER_OFFICIALS_FILE",
    "test-data/officials-test.csv",
)

OFFICIALS_FILE = Path(officials_file_value)

if not OFFICIALS_FILE.is_absolute():
    OFFICIALS_FILE = SCRIPT_DIRECTORY / OFFICIALS_FILE

OFFICIALS_FILE = OFFICIALS_FILE.resolve()


def validate_configuration() -> None:
    """Validate required environment variables and test files."""

    missing_values: list[str] = []

    if not USERNAME:
        missing_values.append(
            "REF_LEDGER_TEST_USERNAME"
        )

    if not PASSWORD:
        missing_values.append(
            "REF_LEDGER_TEST_PASSWORD"
        )

    if not TENANT_ID:
        missing_values.append(
            "REF_LEDGER_TEST_TENANT_ID"
        )

    if missing_values:
        raise RuntimeError(
            "Missing required environment variables: "
            + ", ".join(missing_values)
        )

    if not OFFICIALS_FILE.exists():
        raise FileNotFoundError(
            "Officials import file does not exist: "
            f"{OFFICIALS_FILE}"
        )

    if not OFFICIALS_FILE.is_file():
        raise FileNotFoundError(
            "Officials import path is not a file: "
            f"{OFFICIALS_FILE}"
        )


def login(page: Page) -> None:
    """Log in to Ref Ledger."""

    page.goto(
        f"{BASE_URL}/login",
        wait_until="domcontentloaded",
    )

    print("Before login:", page.url)

    page.locator("#username").fill(
        USERNAME or ""
    )

    page.locator("#password").fill(
        PASSWORD or ""
    )

    with page.expect_response(
        lambda response:
            "/api/login" in response.url
            and response.request.method == "POST",
        timeout=30_000,
    ) as response_info:
        page.get_by_role(
            "button",
            name="Login",
            exact=True,
        ).click()

    login_response = response_info.value

    print(
        "Login response status:",
        login_response.status,
    )

    print(
        "Login response URL:",
        login_response.url,
    )

    if not login_response.ok:
        raise AssertionError(
            "Login failed. "
            f"HTTP status: {login_response.status}"
        )

    page.wait_for_url(
        lambda url: "/login" not in url,
        timeout=15_000,
    )

    page.wait_for_load_state(
        "domcontentloaded"
    )

    print("After login:", page.url)
    print("Page title:", page.title())


def import_officials(
    page: Page,
) -> dict[str, Any]:
    """
    Upload, preview, and import the officials test file.

    Returns the JSON response from the commit endpoint.
    """

    page.goto(
        f"{BASE_URL}/officials",
        wait_until="domcontentloaded",
    )

    print("Officials page:", page.url)

    import_officials_button = page.locator(
        "#importOfficialsBtn"
    )

    import_officials_button.wait_for(
        state="visible",
        timeout=15_000,
    )

    import_officials_button.click()

    page.wait_for_url(
        lambda url:
            "/importOfficials" in url,
        timeout=15_000,
    )

    page.wait_for_load_state(
        "domcontentloaded"
    )

    print("Import page:", page.url)

    file_input = page.locator("#csvFile")

    file_input.wait_for(
        state="attached",
        timeout=10_000,
    )

    file_input.set_input_files(
        str(OFFICIALS_FILE)
    )

    print("Selected file:", OFFICIALS_FILE)

    selected_file_text = (
        page.locator("#selectedFile")
        .inner_text()
    )

    print(
        "Selected file message:",
        selected_file_text,
    )


    preview_button = page.locator(
        "#previewBtn"
    )

    preview_button.wait_for(
        state="visible",
        timeout=15_000,
    )

    if preview_button.is_disabled():
        raise AssertionError(
            "The Preview Officials button is disabled "
            "after selecting the CSV file."
        )

    try:
        with page.expect_response(
            lambda response:
                response.request.method == "POST"
                and response.url.endswith(
                    "/api/import/officials/preview"
                ),
            timeout=30_000,
        ) as preview_response_info:
            preview_button.click()

        preview_response = (
            preview_response_info.value
        )

        print(
            "Preview response status:",
            preview_response.status,
        )

        print(
            "Preview response URL:",
            preview_response.url,
        )

        preview_result = read_json_response(
            preview_response
        )

        print(
            "Preview response:",
            json.dumps(
                preview_result,
                indent=2,
            ),
        )

        if not preview_response.ok:
            error_message = (
                preview_result.get("error")
                or preview_result.get("message")
                or "Unknown preview error"
            )

            raise AssertionError(
                "Officials preview failed. "
                f"HTTP status: "
                f"{preview_response.status}. "
                f"Message: {error_message}"
            )

    except PlaywrightTimeoutError as error:
        page.screenshot(
            path=str(
                SCRIPT_DIRECTORY
                / "import-officials-preview-timeout.png"
            ),
            full_page=True,
        )

        raise AssertionError(
            "Timed out waiting for "
            "/api/import/officials/preview."
        ) from error

    preview_section = page.locator(
        "#previewSection"
    )

    preview_section.wait_for(
        state="visible",
        timeout=15_000,
    )

    total_rows = parse_integer(
        page.locator("#totalRows").inner_text()
    )

    valid_rows = parse_integer(
        page.locator("#validRows").inner_text()
    )

    invalid_rows = parse_integer(
        page.locator("#invalidRows").inner_text()
    )

    print("Preview total rows:", total_rows)
    print("Preview valid rows:", valid_rows)
    print("Preview invalid rows:", invalid_rows)

    if total_rows == 0:
        raise AssertionError(
            "The CSV preview contained zero rows."
        )

    if valid_rows == 0:
        page.screenshot(
            path=str(
                SCRIPT_DIRECTORY
                / "import-officials-no-valid-rows.png"
            ),
            full_page=True,
        )

        raise AssertionError(
            "The CSV preview contained no valid rows. "
            "Review the preview table and CSV contents."
        )

    import_button = page.locator(
        "#importBtn"
    )

    import_button.wait_for(
        state="visible",
        timeout=15_000,
    )

    if import_button.is_disabled():
        raise AssertionError(
            "The import button is disabled "
            "after a successful preview."
        )
    
    try:
        with page.expect_response(
            lambda response:
                response.request.method == "POST"
                and response.url.endswith(
                    "/api/import/officials/commit"
                ),
            timeout=30_000,
        ) as commit_response_info:
            import_button.click()

        commit_response = (
            commit_response_info.value
        )

        print(
            "Import response status:",
            commit_response.status,
        )

        print(
            "Import response URL:",
            commit_response.url,
        )

        commit_result = read_json_response(
            commit_response
        )

        print(
            "Import response:",
            json.dumps(
                commit_result,
                indent=2,
            ),
        )

        if not commit_response.ok:
            error_message = (
                commit_result.get("error")
                or commit_result.get("message")
                or "Unknown import error"
            )

            raise AssertionError(
                "Officials import failed. "
                f"HTTP status: "
                f"{commit_response.status}. "
                f"Message: {error_message}"
            )

    except PlaywrightTimeoutError as error:
        page.screenshot(
            path=str(
                SCRIPT_DIRECTORY
                / "import-officials-commit-timeout.png"
            ),
            full_page=True,
        )

        raise AssertionError(
            "Timed out waiting for "
            "/api/import/officials/commit."
        ) from error

    added = int(
        commit_result.get("added", 0) or 0
    )

    skipped = int(
        commit_result.get("skipped", 0) or 0
    )

    failed = int(
        commit_result.get("failed", 0) or 0
    )

    print("Officials added by endpoint:", added)
    print("Officials skipped:", skipped)
    print("Officials failed:", failed)

    message_text = (
        page.locator("#message").inner_text()
    )

    print("Page result message:", message_text)

    print(
        "Officials import request completed."
    )

    return commit_result


def read_json_response(
    response,
) -> dict[str, Any]:
    """Read a JSON response with a text fallback."""

    try:
        result = response.json()

        if isinstance(result, dict):
            return result

        return {
            "data": result,
        }

    except Exception:
        try:
            text = response.text()
        except Exception:
            text = ""

        return {
            "message": text,
        }


def parse_integer(value: str) -> int:
    """Convert displayed text to an integer."""

    try:
        return int(value.strip())
    except ValueError:
        return 0


def get_official_count() -> int:
    """Return the officials count for the test tenant."""

    client = MongoClient(
        MONGODB_URI,
        serverSelectionTimeoutMS=10_000,
    )

    try:
        client.admin.command("ping")

        collection = (
            client[DATABASE_NAME]["officials"]
        )

        return collection.count_documents(
            {
                "tenantId": TENANT_ID,
            }
        )

    finally:
        client.close()


def handle_request_failed(request) -> None:
    """Print unexpected failed browser requests."""

    ignored_url_parts = (
        "/cdn-cgi/rum",
    )

    if any(
        part in request.url
        for part in ignored_url_parts
    ):
        return

    print(
        "Request failed:",
        request.method,
        request.url,
        request.failure,
    )


@pytest.fixture(scope="session", autouse=True)
def validate_test_configuration() -> None:
    """Validate the test configuration once per pytest session."""

    validate_configuration()
    REPORT_DIRECTORY.mkdir(
        parents=True,
        exist_ok=True,
    )


@pytest.fixture()
def page() -> Page:
    """Create a fresh Playwright page for each test."""

    headless = (
        os.getenv(
            "REF_LEDGER_HEADLESS",
            "false",
        ).strip().lower()
        in {"1", "true", "yes", "on"}
    )

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            headless=headless,
        )

        context = browser.new_context()

        test_page = context.new_page()

        test_page.set_default_timeout(
            15_000
        )

        test_page.on(
            "console",
            lambda message: print(
                f"Browser console "
                f"[{message.type}]:",
                message.text,
            ),
        )

        test_page.on(
            "requestfailed",
            handle_request_failed,
        )

        try:
            yield test_page
        finally:
            context.close()
            browser.close()


def test_import_officials(page: Page) -> None:
    """Verify that importing officials updates MongoDB correctly."""

    count_before = get_official_count()

    print(
        "Officials before import:",
        count_before,
    )

    try:
        login(page)

        import_result = import_officials(
            page
        )

        count_after = get_official_count()

        print(
            "Officials after import:",
            count_after,
        )

        database_count_change = (
            count_after - count_before
        )

        endpoint_added_count = int(
            import_result.get(
                "added",
                0,
            )
            or 0
        )

        skipped_count = int(
            import_result.get(
                "skipped",
                0,
            )
            or 0
        )

        failed_count = int(
            import_result.get(
                "failed",
                0,
            )
            or 0
        )

        print(
            "Database count increase:",
            database_count_change,
        )

        assert failed_count == 0, (
            "The import endpoint reported "
            f"{failed_count} failed row(s)."
        )

        if endpoint_added_count > 0:
            assert (
                database_count_change
                == endpoint_added_count
            ), (
                "The import endpoint reported "
                f"{endpoint_added_count} added "
                "official(s), but the database "
                "count changed by "
                f"{database_count_change}."
            )

        elif skipped_count > 0:
            print(
                "No officials were added because "
                "the test officials already exist."
            )

        else:
            pytest.fail(
                "The import completed but reported "
                "zero added, zero skipped, and "
                "zero failed officials."
            )

    except Exception:
        page.screenshot(
            path=str(
                REPORT_DIRECTORY
                / "import-officials-failure.png"
            ),
            full_page=True,
        )

        raise
