from playwright.sync_api import sync_playwright, expect

import os

USERNAME = os.getenv("REF_LEDGER_TEST_USERNAME")
PASSWORD = os.getenv("REF_LEDGER_TEST_PASSWORD")

with sync_playwright() as p:
    browser = p.chromium.launch(headless=False)
    page = browser.new_page()

    page.goto(
        "https://ref-ledger.com/login",
        wait_until="domcontentloaded"
    )

    print("Before login:", page.url)

    page.locator("#username").fill(USERNAME)
    page.locator("#password").fill(PASSWORD)

    with page.expect_response(
        lambda response: "/api/login" in response.url
    ) as response_info:
        page.get_by_role("button", name="Login").click()

    login_response = response_info.value

    print("Login response status:", login_response.status)
    print("Login response URL:", login_response.url)

    assert login_response.ok, (
        f"Login failed with HTTP {login_response.status}"
    )

    page.wait_for_url(
        "https://ref-ledger.com/",
        timeout=10000
    )

    expect(page).to_have_title("Ref Ledger V2.0")

    print("After login:", page.url)
    print("Page title:", page.title())
    print("TEST PASSED")

    browser.close()