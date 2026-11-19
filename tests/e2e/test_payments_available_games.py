import json
import unittest
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright


PAYMENTS_HTML = (Path(__file__).resolve().parents[2] / "internal" / "html" / "payments.html").read_text(encoding="utf-8")


class AvailableGamesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch()

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()

    def setUp(self):
        self.page = self.browser.new_page()
        self.errors = []
        self.requests = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.page.close()

    def open_payments(self, status, payload):
        def handle(route):
            url = urlparse(route.request.url)
            if url.path == "/payments":
                route.fulfill(content_type="text/html", body=PAYMENTS_HTML)
            elif url.path == "/api/dashboard":
                self.requests.append(url.query)
                route.fulfill(status=status, json=payload)
            elif url.path == "/api/loadAssociations":
                route.fulfill(json=[{"id": "A"}, {"id": "B"}])
            elif url.path == "/api/loadExpenseTypes":
                route.fulfill(json=[{"expenseType": "Fees"}])
            else:
                route.fulfill(status=200, body="")

        self.page.route("**/*", handle)
        self.page.goto("http://payments.test/payments", wait_until="domcontentloaded")
        self.page.wait_for_function("document.querySelector('#gamesBody').children.length > 0")

    def test_completed_games_and_selection(self):
        def game(game_id, association, earnings):
            return {"GameId": game_id, "Date": "11/19/2026", "Time": "1:00 PM",
                    "Sport": "Soccer", "Site": "Field", "Field": "1", "NumOfGames": 1,
                    "Level": "Varsity", "Home": "Home", "Visitor": "Away",
                    "GameFee": f"${earnings}", "TotalEarnings": earnings,
                    "Association": association, "Status": "Completed", "Officials": []}

        self.open_payments(200, [game(101, "A", "75.00"), game(102, "B", "25.50")])
        self.assertEqual(self.requests, ["status=Completed"])
        self.assertEqual(self.errors, [])
        self.page.wait_for_function("document.querySelector('#deductionType').options.length === 2")
        self.assertEqual(self.page.locator("#deductionType option").all_text_contents(),
                         ["Select type when deduction is positive", "Fees"])
        self.assertEqual(self.page.locator(".game-check").count(), 2)
        self.page.locator('.game-check[value="101"]').check()
        self.assertEqual(self.page.locator("#amount").input_value(), "75.00")
        self.assertIn("$100.50", self.page.locator("#message").inner_text())
        self.page.locator("#deductionAmount").fill("5")
        self.assertEqual(self.page.locator("#amount").input_value(), "70.00")
        self.page.locator('.game-check[value="102"]').click()
        self.assertFalse(self.page.locator('.game-check[value="102"]').is_checked())
        self.assertIn("multiple associations", self.page.locator("#message").inner_text())

        self.page.route("**/api/dashboard?*", lambda route: route.fulfill(status=500, body="failure"))
        self.page.evaluate("loadCompletedGames(false)")
        self.assertIn("Unable to load completed games.", self.page.locator("#gamesBody").inner_text())
        self.assertEqual(self.page.locator("#amount").input_value(), "")
        self.assertIn("error", self.page.locator("#message").get_attribute("class").split())

    def test_empty_and_failed_responses(self):
        cases = [
            (404, {"message": "No games were found matching your search criteria."}, "No completed games are available for payment.", False),
            (200, [], "No completed games are available for payment.", False),
            (500, {"message": "failure"}, "Unable to load completed games.", True),
            (404, {"message": "Not found"}, "Unable to load completed games.", True),
            (200, {"games": []}, "Unable to load completed games.", True),
        ]
        for status, payload, expected, is_error in cases:
            with self.subTest(status=status, payload=json.dumps(payload)):
                self.page.unroute_all()
                self.requests.clear()
                self.open_payments(status, payload)
                self.assertIn(expected, self.page.locator("#gamesBody").inner_text())
                self.assertEqual("error" in self.page.locator("#message").get_attribute("class").split(), is_error)


if __name__ == "__main__":
    unittest.main()
