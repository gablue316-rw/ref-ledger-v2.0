package database

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"github.com/go-pdf/fpdf"
	"github.com/resend/resend-go/v4"
)

// Place the supplied font at internal/database/assets/DejaVuSans.ttf.
//
//go:embed assets/DejaVuSans.ttf
var reportEmailFont []byte

var ErrInvalidEmailReport = errors.New("invalid email report request")
var ErrNoEmailReportData = errors.New("no records matched the selected report filters")

type EmailReportRequest struct {
	To          string              `json:"to"`
	Subject     string              `json:"subject"`
	Message     string              `json:"message"`
	Format      string              `json:"format"`
	ReportType  string              `json:"reportType"`
	Filters     map[string][]string `json:"filters"`
	ReportTitle string              `json:"reportTitle"`
	GeneratedAt string              `json:"generatedAt"`
}

func invalidEmailReport(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidEmailReport, message)
}

func validateEmailReport(req EmailReportRequest) error {
	addr, err := mail.ParseAddress(req.To)
	if err != nil || addr.Address != req.To || len(req.To) > 254 {
		return invalidEmailReport("enter one valid recipient email address")
	}
	if req.Subject == "" || len([]rune(req.Subject)) > 200 || strings.ContainsAny(req.Subject, "\r\n") {
		return invalidEmailReport("subject must contain 1 to 200 characters without line breaks")
	}
	if len([]rune(req.Message)) > 5000 {
		return invalidEmailReport("message exceeds 5000 characters")
	}
	if req.Format != "pdf" {
		return invalidEmailReport("only PDF reports are supported")
	}
	allowed := map[string]bool{"type": true, "association": true}
	switch req.ReportType {
	case "financial", "revenue", "expense", "accounts-receivable", "reconciliation":
	case "1099":
		allowed["taxYear"] = true
	case "game":
		for _, key := range []string{"status", "site", "level", "sport", "official", "eco", "assignor", "home", "visitor", "begindate", "enddate"} {
			allowed[key] = true
		}
	default:
		return invalidEmailReport("unsupported report type")
	}
	for key, values := range req.Filters {
		if !allowed[key] {
			return invalidEmailReport("unsupported filter: " + key)
		}
		if len(values) > 200 {
			return invalidEmailReport("too many values for filter: " + key)
		}
		for _, value := range values {
			if len(value) > 512 {
				return invalidEmailReport("filter value is too long")
			}
		}
		switch key {
		case "taxYear", "home", "visitor", "begindate", "enddate", "type":
			if len(values) > 1 {
				return invalidEmailReport("filter requires a single value: " + key)
			}
		}
	}
	if req.ReportType == "1099" && emailFilterValue(req.Filters, "taxYear") != "" {
		year, err := strconv.Atoi(emailFilterValue(req.Filters, "taxYear"))
		if err != nil || year < 1000 || year > 9999 {
			return invalidEmailReport("Tax Year must be between 1000 and 9999")
		}
	}
	return nil
}

func emailFilterValue(filters map[string][]string, key string) string {
	if values := filters[key]; len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

// EmailReport regenerates report data using the authenticated tenant supplied by
// the HTTP handler. It returns the Resend acceptance ID, not a delivery receipt.
func EmailReport(ctx context.Context, tenantID string, req EmailReportRequest) (string, error) {
	req.To = strings.TrimSpace(req.To)
	req.Subject = strings.TrimSpace(req.Subject)
	if tenantID == "" || tenantID == "na" {
		return "", invalidEmailReport("missing authenticated tenant")
	}
	if err := validateEmailReport(req); err != nil {
		return "", err
	}
	apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	if apiKey == "" {
		return "", errors.New("RESEND_API_KEY is not set")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := loadEmailReportData(tenantID, req)
	if err != nil {
		return "", fmt.Errorf("generate report data: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	pdfBytes, err := buildEmailReportPDF(req, data)
	if err != nil {
		return "", err
	}
	// Leave room for base64 expansion and the email envelope.
	if len(pdfBytes) > 20*1024*1024 {
		return "", invalidEmailReport("report is too large; narrow the filters")
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "Your Ref Ledger report is attached."
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sent, err := resend.NewClient(apiKey).Emails.SendWithContext(sendCtx, &resend.SendEmailRequest{
		From: "Ref Ledger <reports@ref-ledger.com>", To: []string{req.To},
		Subject: req.Subject, Text: message,
		Html: "<p>" + strings.ReplaceAll(html.EscapeString(message), "\n", "<br>") + "</p>",
		Attachments: []*resend.Attachment{{
			Filename: "ref-ledger-" + req.ReportType + "-" + time.Now().UTC().Format("20060102") + ".pdf",
			Content:  pdfBytes, ContentType: "application/pdf",
		}},
	})
	if err != nil {
		return "", fmt.Errorf("Resend send failed: %w", err)
	}
	if sent == nil || sent.Id == "" {
		return "", errors.New("Resend returned no email ID")
	}
	return sent.Id, nil
}

func loadEmailReportData(tenantID string, req EmailReportRequest) (interface{}, error) {
	associations := req.Filters["association"]
	switch req.ReportType {
	case "financial":
		return GetFinancialReports(tenantID, associations)
	case "revenue":
		return GetRevenueReports(tenantID, associations)
	case "expense":
		return GetExpenseReports(tenantID, associations)
	case "accounts-receivable":
		return GetAccountsReceivableReport(tenantID, associations)
	case "reconciliation":
		return GetReconciliationReports(tenantID, associations)
	case "1099":
		return Get1099Reports(tenantID, associations, emailFilterValue(req.Filters, "taxYear"))
	case "game":
		begin := emailFilterValue(req.Filters, "begindate")
		end := emailFilterValue(req.Filters, "enddate")
		for _, value := range []string{begin, end} {
			if value != "" {
				if _, err := time.Parse("2006-01-02", value); err != nil {
					return nil, invalidEmailReport("dates must use YYYY-MM-DD")
				}
			}
		}
		if begin != "" && end != "" && begin > end {
			return nil, invalidEmailReport("begin date must not follow end date")
		}
		bDate, eDate, err := utils.FormatDateFilter(begin, end)
		if err != nil {
			return nil, invalidEmailReport("invalid date filters")
		}
		f := req.Filters
		return GetGameReport(model.GameFilter{
			TenantId: tenantID, Association: associations, Status: f["status"],
			Site: f["site"], Level: f["level"], Sport: f["sport"], Assignor: f["assignor"],
			Official: f["official"], ECO: f["eco"], Home: emailFilterValue(f, "home"),
			Visitor: emailFilterValue(f, "visitor"), BeginDate: bDate, EndDate: eDate,
		})
	}
	return nil, invalidEmailReport("unsupported report type")
}

type emailPDFColumn struct {
	label, field, kind string
	width              float64
}
type emailPDFTable struct {
	title, note string
	columns     []emailPDFColumn
	rows        [][]string
}

func emailReportTable(req EmailReportRequest, data interface{}) (emailPDFTable, error) {
	table := emailPDFTable{}
	c := func(label, field, kind string, width float64) emailPDFColumn {
		return emailPDFColumn{label, field, kind, width}
	}
	switch req.ReportType {
	case "financial":
		table.title = "Financial Report"
		table.note = "Deductions are already subtracted from Net Earnings and excluded from this report's expenses. They appear in the Expense Report."
		table.columns = []emailPDFColumn{c("Association", "association", "", 45), c("Net Earnings", "grossIncome", "money", 48), c("Expenses Excluding Deductions", "totalExpenses", "money", 66), c("Net Income Before Taxes", "netIncome", "money", 60), c("Total Mileage", "totalMileage", "number", 48)}
	case "revenue":
		table.title = "Revenue Report"
		table.columns = []emailPDFColumn{c("Association", "association", "", 35), c("Number of Games", "numOfGames", "number", 26), c("Total Game Fees", "totGameFees", "money", 36), c("Total Travel Pay", "totTravelPay", "money", 33), c("Gross Earnings", "grossRevenue", "money", 36), c("Total Assignor Fees", "totAssignorFees", "money", 35), c("Total Deductions", "totDeductions", "money", 32), c("Net Earnings", "netRevenue", "money", 34)}
	case "expense":
		table.title = "Expense Report"
		table.columns = []emailPDFColumn{c("Association", "association", "", 35), c("Food", "food", "money", 30), c("Dues", "dues", "money", 28), c("Camp Fees", "campFees", "money", 33), c("Equipment", "equipment", "money", 34), c("Deductions", "deductions", "money", 34), c("Total Expenses", "totalExpenses", "money", 40), c("Mileage", "mileage", "number", 33)}
	case "accounts-receivable":
		table.title = "Accounts Receivable Report"
		table.columns = []emailPDFColumn{c("Association", "association", "", 50), c("Game IDs", "gameIds", "", 155), c("Accounts Receivable", "accountsReceivable", "money", 62)}
	case "reconciliation":
		table.title = "Reconciliation Report"
		table.columns = []emailPDFColumn{c("Association", "association", "", 32), c("Payment ID", "paymentId", "", 40), c("Game IDs", "paymentGameIds", "", 88), c("Payment Amount", "paymentAmt", "money", 35), c("Calculated Amount", "calculatedAmt", "money", 37), c("Status", "status", "", 35)}
	case "1099":
		table.title = "1099 Report"
		table.columns = []emailPDFColumn{c("Tax Year", "taxYear", "", 40), c("Association", "association", "", 135), c("Total Payments", "amount", "money", 92)}
	case "game":
		table.title = "Games Report"
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return table, err
	}
	var raw interface{}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return table, err
	}
	records := []map[string]interface{}{}
	switch values := raw.(type) {
	case []interface{}:
		for _, value := range values {
			if record, ok := value.(map[string]interface{}); ok {
				records = append(records, record)
			}
		}
	case map[string]interface{}:
		for _, value := range values {
			if record, ok := value.(map[string]interface{}); ok {
				records = append(records, record)
			}
		}
	}
	if len(records) == 0 {
		return table, ErrNoEmailReportData
	}
	if req.ReportType == "game" {
		return table, nil
	}
	sort.SliceStable(records, func(i, j int) bool {
		a, b := emailPDFValue(records[i]["association"]), emailPDFValue(records[j]["association"])
		if a != b {
			return a < b
		}
		if req.ReportType == "1099" {
			return emailPDFValue(records[i]["taxYear"]) < emailPDFValue(records[j]["taxYear"])
		}
		return emailPDFValue(records[i]["paymentId"]) < emailPDFValue(records[j]["paymentId"])
	})
	totals := make([]int64, len(table.columns))
	for _, record := range records {
		if req.ReportType == "expense" {
			var cents int64
			for _, field := range []string{"food", "dues", "campFees", "equipment", "deductions"} {
				n, err := emailPDFCents(emailPDFValue(record[field]))
				if err != nil {
					return table, err
				}
				cents += n
			}
			record["totalExpenses"] = emailPDFMoney(cents)
		}
		row := make([]string, len(table.columns))
		for i, column := range table.columns {
			value := emailPDFValue(record[column.field])
			switch column.kind {
			case "money":
				n, err := emailPDFCents(value)
				if err != nil {
					return table, err
				}
				totals[i] += n
				value = emailPDFMoney(n)
			case "number":
				n, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", ""), 64)
				if err != nil {
					return table, fmt.Errorf("invalid %s: %w", column.field, err)
				}
				totals[i] += int64(n*100 + 0.5)
				value = strconv.FormatFloat(n, 'f', -1, 64)
			}
			row[i] = value
		}
		table.rows = append(table.rows, row)
	}
	total := make([]string, len(table.columns))
	total[0] = "Grand Total"
	for i, column := range table.columns {
		if column.kind == "money" {
			total[i] = emailPDFMoney(totals[i])
		}
		if column.kind == "number" {
			total[i] = strconv.FormatFloat(float64(totals[i])/100, 'f', -1, 64)
		}
	}
	table.rows = append(table.rows, total)
	return table, nil
}

func emailPDFValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if values, ok := value.([]interface{}); ok {
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = emailPDFValue(v)
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(value)
}

// Parse formatted monetary values without summing floating-point dollars.
func emailPDFCents(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "$", ""), ",", ""))
	if value == "" {
		return 0, errors.New("missing monetary value")
	}
	negative := strings.HasPrefix(value, "-") || (strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")"))
	value = strings.Trim(value, "-()")
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid monetary value: %s", value)
	}
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || dollars < 0 {
		return 0, fmt.Errorf("invalid monetary value: %s", value)
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) > 2 {
			return 0, fmt.Errorf("invalid monetary precision: %s", value)
		}
		fraction += strings.Repeat("0", 2-len(fraction))
	}
	cents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, err
	}
	if dollars > (1<<63-1-cents)/100 {
		return 0, errors.New("monetary value overflow")
	}
	result := dollars*100 + cents
	if negative {
		result = -result
	}
	return result, nil
}

func emailPDFMoney(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	dollars := strconv.FormatInt(cents/100, 10)
	for i := len(dollars) - 3; i > 0; i -= 3 {
		dollars = dollars[:i] + "," + dollars[i:]
	}
	return fmt.Sprintf("%s$%s.%02d", sign, dollars, cents%100)
}

func buildEmailReportPDF(req EmailReportRequest, data interface{}) ([]byte, error) {
	table, err := emailReportTable(req, data)
	if err != nil {
		return nil, err
	}
	orientation := "L"
	if req.ReportType == "game" {
		orientation = "P"
	}
	pdf := fpdf.New(orientation, "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(false, 15)
	pdf.AddUTF8FontFromBytes("report", "", reportEmailFont)
	pdf.SetTitle("Ref Ledger - "+table.title, true)
	pdf.SetAuthor("Ref Ledger", true)
	pdf.SetHeaderFunc(func() {
		pdf.SetTextColor(30, 41, 59)
		pdf.SetFont("report", "", 17)
		pdf.CellFormat(0, 9, "Ref Ledger | "+table.title, "", 1, "L", false, 0, "")
		pdf.SetTextColor(100, 116, 139)
		pdf.SetFont("report", "", 8)
		pdf.CellFormat(0, 5, "Generated "+time.Now().UTC().Format("2006-01-02 15:04 UTC"), "", 1, "L", false, 0, "")
		pdf.Ln(3)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("report", "", 8)
		pdf.SetTextColor(100, 116, 139)
		pdf.CellFormat(0, 5, fmt.Sprintf("Ref Ledger  |  Page %d", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetFont("report", "", 9)
	pdf.SetTextColor(51, 65, 85)
	_, pageHeight := pdf.GetPageSize()
	line := func(text string) {
		for _, part := range pdf.SplitText(text, func() float64 { w, _ := pdf.GetPageSize(); return w - 30 }()) {
			if pdf.GetY()+5 > pageHeight-18 {
				pdf.AddPage()
				pdf.SetFont("report", "", 9)
				pdf.SetTextColor(51, 65, 85)
			}
			pdf.CellFormat(0, 5, part, "", 1, "L", false, 0, "")
		}
	}
	keys := make([]string, 0, len(req.Filters))
	for key := range req.Filters {
		if key != "type" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		line("Filters: All associations")
	}
	filterLabels := map[string]string{"association": "Associations", "status": "Status", "site": "Sites", "level": "Levels", "sport": "Sports", "official": "Officials", "eco": "ECO", "assignor": "Assignors", "home": "Home", "visitor": "Visitor", "begindate": "Begin Date", "enddate": "End Date", "taxYear": "Tax Year"}
	for _, key := range keys {
		line(filterLabels[key] + ": " + strings.Join(req.Filters[key], ", "))
	}
	pdf.Ln(4)
	if req.ReportType == "game" {
		games, ok := data.([]model.GameView)
		if !ok {
			return nil, errors.New("unexpected games report data")
		}
		sort.SliceStable(games, func(i, j int) bool {
			a, _ := time.Parse("1/2/2006", emailPDFDate(games[i].Date))
			b, _ := time.Parse("1/2/2006", emailPDFDate(games[j].Date))
			if !a.Equal(b) {
				return a.Before(b)
			}
			if games[i].Time != games[j].Time {
				return games[i].Time < games[j].Time
			}
			return games[i].GameId < games[j].GameId
		})
		var totalGames, totalEarnings int64
		for _, game := range games {
			totalGames += game.NumOfGames
			cents, err := emailPDFCents(game.TotalEarnings)
			if err != nil {
				return nil, err
			}
			totalEarnings += cents
			for _, field := range []*string{&game.GameFee, &game.TravelPay, &game.AssignorFee, &game.Deductions, &game.TotalEarnings} {
				amount, err := emailPDFCents(*field)
				if err != nil {
					return nil, err
				}
				*field = emailPDFMoney(amount)
			}
			if game.Assignor == "" {
				game.Assignor = "Unassigned"
			}
			if pdf.GetY()+65 > pageHeight-18 {
				pdf.AddPage()
			}
			pdf.SetFont("report", "", 11)
			pdf.SetTextColor(30, 41, 59)
			line(fmt.Sprintf("%s | Game %d | %s %s", game.Association, game.GameId, game.Date, game.Time))
			pdf.SetFont("report", "", 9)
			for _, text := range []string{
				"Sport: " + game.Sport + " | Level: " + game.Level + " | Status: " + game.Status,
				"Site: " + game.Site + " | Field: " + game.Field,
				"Home: " + game.Home + " | Visitor: " + game.Visitor,
				"Assignor: " + game.Assignor,
				fmt.Sprintf("Number of Games: %d | Game Fee: %s | Travel Pay: %s", game.NumOfGames, game.GameFee, game.TravelPay),
				"Assignor Fee: " + game.AssignorFee + " | Deductions: " + game.Deductions + " | Earnings: " + game.TotalEarnings,
			} {
				line(text)
			}
			officials := append([]model.OfficialView(nil), game.Officials...)
			sort.SliceStable(officials, func(i, j int) bool { return officials[i].DisplayOrder < officials[j].DisplayOrder })
			for _, official := range officials {
				line(official.Title + ": " + official.Name)
			}
			if len(officials) == 0 && game.ECO != "" {
				line("ECO: " + game.ECO)
			}
			pdf.Ln(5)
		}
		line(fmt.Sprintf("Grand Total: %d assignments | %d games | Earnings: %s", len(games), totalGames, emailPDFMoney(totalEarnings)))
	} else {
		drawEmailPDFTable(pdf, table)
		if table.note != "" {
			pdf.Ln(4)
			pdf.SetFont("report", "", 9)
			line(table.note)
		}
	}
	var result bytes.Buffer
	if err := pdf.Output(&result); err != nil {
		return nil, fmt.Errorf("create PDF: %w", err)
	}
	return result.Bytes(), nil
}

// Split tall rows across pages, repeating the column headings on every page.
func drawEmailPDFTable(pdf *fpdf.Fpdf, table emailPDFTable) {
	_, pageHeight := pdf.GetPageSize()
	pdf.SetFont("report", "", 8)
	draw := func(cells [][]string, start, count int, height float64) {
		x, y := 15.0, pdf.GetY()
		for i, column := range table.columns {
			pdf.Rect(x, y, column.width, height, "DF")
			for n := 0; n < count; n++ {
				if start+n < len(cells[i]) {
					pdf.SetXY(x+2, y+2+float64(n)*4)
					pdf.CellFormat(column.width-4, 4, cells[i][start+n], "", 0, "L", false, 0, "")
				}
			}
			x += column.width
		}
		pdf.SetXY(15, y+height)
	}
	header := func() {
		pdf.SetFont("report", "", 8)
		pdf.SetFillColor(30, 41, 59)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetDrawColor(203, 213, 225)
		cells := make([][]string, len(table.columns))
		count := 1
		for i, column := range table.columns {
			cells[i] = pdf.SplitText(column.label, column.width-4)
			if len(cells[i]) > count {
				count = len(cells[i])
			}
		}
		draw(cells, 0, count, float64(count)*4+4)
		pdf.SetTextColor(51, 65, 85)
	}
	if pdf.GetY()+20 > pageHeight-18 {
		pdf.AddPage()
	}
	header()
	for rowIndex, row := range table.rows {
		cells := make([][]string, len(row))
		count := 1
		for i, value := range row {
			cells[i] = pdf.SplitText(value, table.columns[i].width-4)
			if len(cells[i]) > count {
				count = len(cells[i])
			}
		}
		for start := 0; start < count; {
			available := int((pageHeight - 18 - pdf.GetY() - 4) / 4)
			if available < 1 {
				pdf.AddPage()
				header()
				available = int((pageHeight - 18 - pdf.GetY() - 4) / 4)
			}
			remaining := count - start
			if start == 0 && remaining > available && remaining < 35 {
				pdf.AddPage()
				header()
				available = int((pageHeight - 18 - pdf.GetY() - 4) / 4)
			}
			n := remaining
			if n > available {
				n = available
			}
			pdf.SetFont("report", "", 8)
			pdf.SetTextColor(51, 65, 85)
			if rowIndex == len(table.rows)-1 {
				pdf.SetFillColor(226, 232, 240)
			} else if rowIndex%2 == 0 {
				pdf.SetFillColor(248, 250, 252)
			} else {
				pdf.SetFillColor(255, 255, 255)
			}
			draw(cells, start, n, float64(n)*4+4)
			start += n
		}
	}
}

func emailPDFDate(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
