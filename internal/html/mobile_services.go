package main

import (
	"net/http"
)

func MobileReports(w http.ResponseWriter, r *http.Request) {
	handler := map[string]http.HandlerFunc{
		"game":                GenerateGameReport,
		"financial":           GenerateFinancialReport,
		"revenue":             GenerateRevenueReport,
		"1099":                Generate1099Report,
		"expense":             GenerateExpenseReport,
		"accounts-receivable": GenerateAccountsReceivableReport,
		"reconciliation":      GenerateReconciliationReport,
	}[r.PathValue("type")]
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler(w, r)
}

func MobilePayments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var handler http.HandlerFunc
	switch r.Method {
	case http.MethodGet:
		handler = LoadPaymentRegistry
	case http.MethodPost:
		handler = CreatePayment
	case http.MethodPut:
		handler = UpdatePayment
	case http.MethodDelete:
		handler = DeletePayment
	default:
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handler(w, r)
}

func MobileSupportedSports(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		LoadSportsSupportedHandler(w, r)
	case http.MethodPost:
		SaveSportsSupportedHandler(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
