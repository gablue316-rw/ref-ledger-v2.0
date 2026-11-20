package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMobileReportsRejectsUnknownReport(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/mobile/reports/unknown", nil)
	req.SetPathValue("type", "unknown")
	res := httptest.NewRecorder()

	MobileReports(res, req)

	if res.Code != 404 {
		t.Fatalf("status = %d, want 404", res.Code)
	}
}

func TestMobilePaymentsRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest("PATCH", "/api/mobile/payments", nil)
	res := httptest.NewRecorder()

	MobilePayments(res, req)

	if res.Code != 405 {
		t.Fatalf("status = %d, want 405", res.Code)
	}
	if got := res.Header().Get("Allow"); got != "GET, POST, PUT, DELETE" {
		t.Fatalf("Allow = %q", got)
	}
}

func TestMobileServiceHandlersRejectCookieAndClientTenant(t *testing.T) {
	t.Setenv("MAINTENANCE_MODE", "false")
	handlers := []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{"GET", "/api/mobile/reports/game", MobileReports},
		{"GET", "/api/mobile/payments", MobilePayments},
		{"POST", "/api/mobile/payments", MobilePayments},
		{"GET", "/api/mobile/expense-types", GetExpenseTypes},
		{"GET", "/api/mobile/settings/sports", GetSportsHandler},
		{"GET", "/api/mobile/settings/supported-sports", MobileSupportedSports},
		{"POST", "/api/mobile/settings/supported-sports", MobileSupportedSports},
		{"GET", "/api/mobile/settings/levels", LevelsHandler},
		{"POST", "/api/mobile/settings/level-active", ActivateLevel},
		{"POST", "/api/mobile/settings/delete-records", DeleteRecordsHandler},
	}
	for _, testCase := range handlers {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			req := httptest.NewRequest(testCase.method, testCase.path+"?tenantId=other", nil)
			req.AddCookie(&http.Cookie{Name: "rl_session", Value: "web-session"})
			res := httptest.NewRecorder()

			mobileAccountHandler(testCase.handler).ServeHTTP(res, req)

			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", res.Code)
			}
		})
	}
}
