package reports

import (
	"context"
	"fmt"
	"math"
	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
)

func TrimMileageStr(miles string) string {

	//
	// Truncate the .00 if it exists
	//
	m := strings.Split(miles, ".")
	return m[0]

}

func ConvertTrueMilesToInt64Rate(miles int64) int64 {

	trueMiles := miles / 100
	temp := float64(trueMiles) * 725 / 10
	rounded := math.Ceil(temp)
	return int64(rounded)
}

func ConvertInt64MilesToExpense(miles int64) string {

	trueMiles := miles / 100
	milesExp := trueMiles * 725
	expense := float32(milesExp) / 10
	expenseStr := utils.ConvertInt64ToAmtStr(int64(expense))
	return expenseStr
}

func ConvertMilesToExpense(miles string) (string, int64) {

	//
	// Truncate the .00 if it exists
	//
	m := strings.Split(miles, ".")

	//
	// Convert string to int64 so we can calculate mileage expense
	//
	amt, err := utils.ConvertStrToInt64(m[0])
	if err != nil {
		fmt.Println(err)
		return "", 0
	}

	//
	// Convert miles to expense
	//

	temp := float64(amt) * 725 / 10
	rounded := math.Ceil(temp)
	expense := int64(rounded)
	expenseStr := utils.ConvertInt64ToAmtStr(expense)
	return expenseStr, expense
}

var mileageRate float32 = 0.725

type AssociationExpenses struct {
	TotalExpenses map[string]map[string]int64
}

type GameTotals struct {
	NumOfGames int64
	GameFees   int64
}

type AssocGameTotalsMap struct {
	AssocGameTotals map[string]map[string]*GameTotals
}

func (a *AssociationExpenses) Init() {

	a.TotalExpenses = make(map[string]map[string]int64)

	firstKeys := database.Associations
	secondKeys := database.ExpenseTypes

	for _, fk := range firstKeys {
		a.TotalExpenses[fk] = make(map[string]int64)
		for _, sk := range secondKeys {
			a.TotalExpenses[fk][sk] = 0
		}
	}
}

func (a *AssociationExpenses) UpdateExpense(assoc, expense string, amount int64) {

	if a.TotalExpenses[assoc] == nil {
		return
	}

	a.TotalExpenses[assoc][expense] += amount
}

func (a *AssociationExpenses) GetTotalExpenseByType(expenseType string) int64 {

	firstKeys := database.Associations
	secondKey := expenseType
	var totalExpenses int64 = 0

	for _, fk := range firstKeys {
		totalExpenses += a.TotalExpenses[fk][secondKey]
	}

	return totalExpenses
}

func (a *AssociationExpenses) GetExpenseByType(assoc, expenseType string) int64 {

	if a.TotalExpenses[assoc] == nil {
		return 0
	}

	return a.TotalExpenses[assoc][expenseType]
}

func (a *AssociationExpenses) GetTotalExpensesByAssociation(assoc string) int64 {

	var totalExpenses int64 = 0
	if a.TotalExpenses[assoc] == nil {
		return totalExpenses
	}

	firstKey := assoc
	secondKeys := database.ExpenseTypes

	for _, sk := range secondKeys {
		if sk == "Mileage" {
			// Skip for now
			continue
			//totalExpenses += ConvertTrueMilesToInt64Rate(a.TotalExpenses[firstKey][sk])
		} else {
			totalExpenses += a.TotalExpenses[firstKey][sk]
		}
	}

	return totalExpenses
}

func (g *AssocGameTotalsMap) Init() {

	g.AssocGameTotals = make(map[string]map[string]*GameTotals)

	firstKeys := database.Associations
	secondKeys := database.PermittedGameStatusValues

	for _, fk := range firstKeys {
		g.AssocGameTotals[fk] = make(map[string]*GameTotals)
		for _, sk := range secondKeys {
			g.AssocGameTotals[fk][sk] = &GameTotals{}
		}
	}

}

func (g *AssocGameTotalsMap) Update(assoc, status string, numOfGames, gameFee int64) error {

	if g.AssocGameTotals[assoc] == nil {
		return fmt.Errorf("Association %s not found in table", assoc)
	}

	if g.AssocGameTotals[assoc][status] == nil {
		return fmt.Errorf("Status %s not found in table", status)
	}

	g.AssocGameTotals[assoc][status].NumOfGames += numOfGames
	g.AssocGameTotals[assoc][status].GameFees += gameFee

	return nil
}

func (g *AssocGameTotalsMap) FormatTotalLine() []string {

	var reptLines []string = []string{}
	assocTotalLine := "    Association: %-12s Game Status: %-11s Total Number of Games: %-4d  Total Game Fees: $%s\n"

	firstKeys := database.Associations
	secondKeys := database.PermittedGameStatusValues

	for _, fk := range firstKeys {
		for _, sk := range secondKeys {
			if g.AssocGameTotals[fk][sk].NumOfGames == 0 {
				continue
			}
			reptLines = append(reptLines, fmt.Sprintf(assocTotalLine, fk, sk, g.AssocGameTotals[fk][sk].NumOfGames, utils.ConvertInt64ToAmtStr(g.AssocGameTotals[fk][sk].GameFees)))
		}
	}
	return reptLines
}

func getReportGeneratedDate() string {

	currentTime := time.Now()
	reportDate := currentTime.Format("01-02-2006")
	reportTime := currentTime.Format("15:04:05")
	reptTimeMsg := "Report generated on " + reportDate + " at " + reportTime + "\n\n"

	return reptTimeMsg
}

func FormatOfficialString(ref, u1, u2 string) string {

	officialStr := ""
	refStr := ""
	u1Str := ""
	u2Str := ""

	if ref != "" && ref != "Unassigned" {
		refList := strings.Split(ref, " ")
		refStr = string(ref[0]) + ". " + refList[1]
	}

	if u1 != "" && u1 != "Unassigned" {
		u1List := strings.Split(u1, " ")
		u1Str = string(u1[0]) + ". " + u1List[1]
	}

	if u2 != "" && u2 != "Unassigned" {
		u2List := strings.Split(u2, " ")
		u2Str = string(u2[0]) + ". " + u2List[1]
	}

	if refStr != "" {
		officialStr = refStr
	}

	if u1Str != "" {
		if len(officialStr) > 0 {
			officialStr += "/" + u1Str
		} else {
			officialStr = u1Str
		}
	}

	if u2Str != "" {
		if len(officialStr) > 0 {
			officialStr += "/" + u2Str
		} else {
			officialStr = u2Str
		}
	}

	return officialStr

}

func formatAddress(address, city, state, zip string) string {

	addr := fmt.Sprintf("%s, %s, %s. %s", address, city, state, zip)

	if len(addr) == 6 {
		return ""
	}

	return addr
}

func WriteReportToFile(report []string, filePath string) error {

	pdf := gofpdf.New("L", "mm", "Letter", "") // Landscape
	pdf.AddPage()

	pdf.SetFont("Courier", "", 8)

	for _, line := range report {
		pdf.CellFormat(0, 4, line, "", 1, "", false, 0, "")
	}

	err := pdf.OutputFileAndClose(filePath)
	if err != nil {
		fmt.Errorf("Failed to write report to PDF file.  Reason: %v", err)
	}

	return nil

	/*
		   flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC

			fd, err := os.OpenFile(filePath, flag, 0644)
			if err != nil {
				fmt.Println(err)
			}

			defer fd.Close()

			for _, s := range report {
				_, err = fd.WriteString(s)
			}
	*/
}

func PrintReport(report []string) {

	for _, v := range report {
		fmt.Printf("%s", v)
	}

}

func CalculateGameFee(gameRec model.GameDescriptor) int64 {

	var gameFee int64
	var gFee int64
	var numOfGames int64
	var travelPay int64
	var deductions int64
	var assignorFee int64

	gFee, _ = utils.ConvertAmtStrToInt64(gameRec.GameFee)
	numOfGames, _ = utils.ConvertStrToInt64(gameRec.NumOfGames)
	travelPay, _ = utils.ConvertAmtStrToInt64(gameRec.TravelPay)
	deductions, _ = utils.ConvertAmtStrToInt64(gameRec.Deductions)
	assignorFee, _ = utils.ConvertAmtStrToInt64(gameRec.AssignorFee)

	gameFee = gFee*numOfGames + travelPay - deductions - assignorFee

	return gameFee
}

func GenerateReconciliationReport(records []model.PaymentDescriptor) []string {

	var totalPayments int64 = 0
	var paymentAmtInt64 int64 = 0
	var calcPayment int64 = 0
	var calcPaymentStr string = ""
	var totalNumOfPayments int = 0
	var err error
	var status string = "Reconciled"

	reptFmtStr := "%-21s%-19s%-17s$%-19s$%-19s%-16s\n"
	gameIds := []int64{}
	rept := []string{}

	title := "Reconciliation Report\n"
	heading1 := "Association          Payment ID         Payment Date     Payment Amount      Calculated Amount   Status\n"
	separator := "============================================================================================================\n"
	reptTimeMsg := getReportGeneratedDate()

	maxLineLength := len(heading1)
	newTitle := utils.CenterText(title, maxLineLength)
	newReptTimeMsg := utils.CenterText(reptTimeMsg, maxLineLength)

	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading1)
	rept = append(rept, separator)

	var missingGames []string = []string{}

	assocMap := make(map[string]bool)

	for _, record := range records {

		paymentAmtInt64, err = utils.ConvertAmtStrToInt64(record.PaymentAmt)
		if err != nil {
			utils.AuditLog.Printf("Failed to convert payment amount string to int64 for PaymentId %s.  Reason: %v", record.PaymentId, err)
			continue
		}

		gameIds, err = utils.ConvertGameIdStrToInt(record.GameIds)
		if err != nil {
			continue
		}

		calcPayment, err = database.GetGameFee(gameIds)
		if err != nil {
			utils.AuditLog.Printf("Failed to get game fee for PaymentId %s.  Reason: %v", record.PaymentId, err)
			continue
		}

		totalPayments += paymentAmtInt64
		totalNumOfPayments++

		if paymentAmtInt64 > calcPayment {
			status = "Overpaid"
		} else if paymentAmtInt64 < calcPayment {
			status = "Underpaid"
		} else {
			status = "Reconciled"
		}

		calcPaymentStr = utils.ConvertInt64ToAmtStr(calcPayment)
		rept = append(rept, fmt.Sprintf(reptFmtStr, record.Association, record.PaymentId, record.PaymentDate, record.PaymentAmt, calcPaymentStr, status))

		assocMap[record.Association] = true

	}

	rept = append(rept, "\n")
	rept = append(rept, fmt.Sprintf("Total Payments: %d Total Deposits: $%s\n", totalNumOfPayments, utils.ConvertInt64ToAmtStr(totalPayments)))

	for key := range assocMap {
		missingGames = getMissingPaidGames(key)
		if len(missingGames) > 0 {
			rept = append(rept, missingGames...)
			missingGames = []string{}
		}
	}

	return rept
}

func GeneratePaymentReport(records []model.PaymentDescriptor) []string {

	fmt.Println("Generating Payment Report")
	rept := make([]string, 10, 20)
	var totalPayments int64 = 0
	var paymentAmtInt64 int64 = 0
	var totalNumOfPayments int = 0
	var err error

	reptFmtStr := "%-19s%-17s$%-11s%-16s%-60s\n"
	reptFmtStr2 := "%-64s%-60s\n"

	title := "Game Report\n"
	heading1 := "Payment ID         Payment Date     Amount      Association     Game IDs\n"
	separator := "===================================================================================================================================\n"
	reptTimeMsg := getReportGeneratedDate()

	maxLineLength := len(heading1)
	newTitle := utils.CenterText(title, maxLineLength)
	newReptTimeMsg := utils.CenterText(reptTimeMsg, maxLineLength)

	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading1)
	rept = append(rept, separator)

	for _, record := range records {

		totalNumOfPayments++
		paymentAmtInt64, err = utils.ConvertAmtStrToInt64(record.PaymentAmt)
		if err == nil {
			totalPayments += paymentAmtInt64
		} else {
			utils.AuditLog.Printf("Failed to convert payment amount string to int64 for PaymentId %s.  Reason: %v", record.PaymentId, err)
		}

		if len(record.GameIds) > 60 {
			gameIdLines := utils.FormatGameIdStrSplice(record.GameIds, 60)

			for i, v := range gameIdLines {
				if i == 0 {
					rept = append(rept, fmt.Sprintf(reptFmtStr, record.PaymentId, record.PaymentDate, record.PaymentAmt, record.Association, v))
				} else {
					rept = append(rept, fmt.Sprintf(reptFmtStr2, "", v))
				}
			}
		} else {
			rept = append(rept, fmt.Sprintf(reptFmtStr, record.PaymentId, record.PaymentDate, record.PaymentAmt, record.Association, record.GameIds))
		}
	}
	rept = append(rept, "\n")
	rept = append(rept, fmt.Sprintf("Total Payments: %d Total Deposits: $%s\n", totalNumOfPayments, utils.ConvertInt64ToAmtStr(totalPayments)))

	return rept
}

func GenerateOfficialsReport(records []model.OfficialDescriptor) []string {

	fmt.Println("Generating Officials Report")
	var title string = "Officials Report\n"
	var rept []string = []string{}

	reptTimeMsg := getReportGeneratedDate()
	heading1 := "Official Id     Officials Name             Phone Number    Association\n"
	separator := "======================================================================\n"

	reptFmt := "%-16d%-28s%-15s%-15s\n"

	maxLineLength := len(heading1)

	rept = append(rept, utils.CenterText(title, maxLineLength))
	rept = append(rept, utils.CenterText(reptTimeMsg, maxLineLength))
	rept = append(rept, heading1)
	rept = append(rept, separator)

	totalOfficials := 0
	for _, r := range records {
		name := fmt.Sprintf("%s %s", r.FirstName, r.LastName)
		rept = append(rept, fmt.Sprintf(reptFmt, r.OfficialId, name, r.Phone, r.Association))
		totalOfficials++
	}

	rept = append(rept, fmt.Sprintf("\nTotal Officials:%d", totalOfficials))

	return rept
}

func GenerateAcctsRecvReport(parentCtx context.Context, associations string) []string {

	fmt.Println("Generating Accounts Receivable Report")
	rept := make([]string, 10, 20)

	title := "Accounts Receivable Report\n"
	reptTimeMsg := getReportGeneratedDate()
	heading1 := "Association    Accounts Receivable   Game IDs\n"
	separator := "=======================================================================================================================\n"
	totalSeparator := "\n----------------------------------------------------------------------------------------------------------------------\n"

	totAcctRptFormat := "\nTotal Accounts Receivable: $%s Total Game IDs: %d"
	reptFmt := "%-15s$%-22s%-60s\n"

	maxLineLength := len(heading1)

	rept = append(rept, utils.CenterText(title, maxLineLength))
	rept = append(rept, utils.CenterText(reptTimeMsg, maxLineLength))
	rept = append(rept, heading1)
	rept = append(rept, separator)

	if associations == "" {
		assocs, err := api.GetAssociations(parentCtx)
		if err != nil {
			fmt.Println("Error:", err)
			return nil
		}
		associations = assocs
	}
	associationList := strings.Split(associations, ",")

	grandTot := int64(0)
	totalGameId := 0

	for _, assoc := range associationList {

		gFilter := model.GFilters{
			Status:      "Completed",
			Association: assoc,
		}

		gFilters, err := utils.ConvertGameFiltersToJsonFile(gFilter)
		if err != nil {
			fmt.Println("Error:", err)
			return nil
		}
		fmt.Println("gFilters", gFilters)

		gameRecords, err := database.QueryAggregatedGames(parentCtx, "refLedger_v2", "games", gFilters)

		if err != nil {
			fmt.Println("Error:", err)
			return nil
		}

		gameIds := []int64{}
		acctsRecv := int64(0)

		for _, r := range gameRecords {

			g, err := utils.ConvertStrToInt64(r.GameId)

			if err != nil {
				fmt.Println(err)
				continue
			}

			gameIds = append(gameIds, g)
			gFee, _ := utils.ConvertAmtStrToInt64(r.GameFee)
			acctsRecv += gFee
		}

		gameIdRange, _ := utils.ConvertGameIdsToRange(gameIds)
		rept = append(rept, fmt.Sprintf(reptFmt, assoc, utils.ConvertInt64ToAmtStr(acctsRecv), gameIdRange))
		grandTot += acctsRecv
		totalGameId += len(gameIds)
	}

	if totalGameId > 0 {
		rept = append(rept, totalSeparator)
		rept = append(rept, fmt.Sprintf(totAcctRptFormat, utils.ConvertInt64ToAmtStr(grandTot), totalGameId))
	}

	return rept
}

func getMissingPaidGames(assoc string) []string {

	var gamesInPaidStatusSet []int64 = []int64{}
	var gamesPaidForSet []int64 = []int64{}
	var missingGamesStr []string = []string{}

	var gamesMissingPayment []int64 = []int64{}

	gamesInPaidStatusSet, err := database.GetGamesInPaidStatusPerAssoc(assoc)
	if err != nil {
		fmt.Println(err)
		return missingGamesStr
	}

	gamesPaidForSet, err = database.GetGamesPaidForPerAssoc(assoc)
	if err != nil {
		fmt.Println(err)
		return missingGamesStr
	}

	paidFor := make(map[int64]bool)

	for _, g := range gamesPaidForSet {
		paidFor[g] = true
	}

	for _, g := range gamesInPaidStatusSet {
		if !paidFor[g] {
			gamesMissingPayment = append(gamesMissingPayment, g)
		}
	}

	if len(gamesMissingPayment) > 0 {
		missingGamesStr = append(missingGamesStr, fmt.Sprintf("\nThe following game ids are in paid status without any record of payment for Association %s\n\n", assoc))
		for _, id := range gamesMissingPayment {
			missingGamesStr = append(missingGamesStr, fmt.Sprintf("     %d\n", id))
		}
	}

	return missingGamesStr
}

func GenerateIncomeReport(assoc string) []string {

	fmt.Println("Generating Income Report")
	rept := []string{}
	expenseRpt := []string{}
	title := "Income Report\n"
	expenseRptTitle := "Detail Expense Report\n\n"

	reptTimeMsg := getReportGeneratedDate()
	assocList := []string{}

	if assoc == "" {
		assocList = database.Associations
	} else {
		assocList = strings.Split(assoc, ",")
	}

	heading1 := "               Total      Total     Total      Gross         Total         Total          Net         Total     Total  \n"
	heading2 := "Association    Games    Game Fees   Travel    Revenue    Assignor Fees   Deductions     Revenue     Expenses    Profit \n"
	separator := "=======================================================================================================================\n"
	separator2 := "_______________________________________________________________________________________________________________________\n"
	reptFmtStr := "%-15s%-9s$%-11s$%-9s$%-10s$%-15s$%-14s$%-11s$%-11s$%-10s\n"
	expReptFmtStr := "%-15s$%-9s$%-14s%-11s$%-11s$%-14s$%-14s\n"

	expenseHeading1 := "               Total       Total        Total      Total         Total         Total\n"
	expenseHeading2 := "Association    Dues      Camp Fees      Miles      Food        Equipment      Expenses\n"
	expenseSeparator := "============================================================================================\n"
	expenseSeparator2 := "___________________________________________________________________________________________\n"

	maxLineLength := len(heading1)

	newTitle := utils.CenterText(title, maxLineLength)
	newReptTimeMsg := utils.CenterText(reptTimeMsg, maxLineLength)
	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading1)
	rept = append(rept, heading2)
	rept = append(rept, separator)

	maxLineLength = len(expenseHeading1)
	newTitle = utils.CenterText(expenseRptTitle, maxLineLength)
	expenseRpt = append(expenseRpt, newTitle)
	expenseRpt = append(expenseRpt, expenseHeading1)
	expenseRpt = append(expenseRpt, expenseHeading2)
	expenseRpt = append(expenseRpt, expenseSeparator)

	totAssignorFees := int64(0)
	totDeductions := int64(0)
	totTravelPay := int64(0)
	totGameFees := int64(0)

	totDues := int64(0)
	totCampFees := int64(0)
	totFood := int64(0)
	totEquipment := int64(0)
	totMiles := int64(0)
	totGrossRev := int64(0)
	totNetRev := int64(0)
	netProfit := int64(0)
	totNetProfit := int64(0)
	grandTotExpenses := int64(0)
	totGames := int64(0)

	totExpenses := int64(0)

	for _, a := range assocList {

		numOfGames, err := database.GetTotalGames(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		assignorFees, err := database.GetTotalAssignorFee(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		travelPay, err := database.GetTotalTravelPay(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		deductions, err := database.GetTotalDeductions(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		dues, err := database.GetTotalDues(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		miles, err := database.GetTotalMileage(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		gameFees, err := database.GetTotalGrossGameFee(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		food, err := database.GetTotalFoodExpense(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		campFees, err := database.GetTotalCampFees(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		equipmentFees, err := database.GetTotalEquipmentExpense(a)
		if err != nil {
			fmt.Println(err)
			return []string{}
		}

		grossRev := gameFees + travelPay
		netRev := grossRev - assignorFees - deductions
		totGrossRev += grossRev
		totNetRev += netRev
		totFood += food
		totGameFees += gameFees
		totAssignorFees += assignorFees
		totTravelPay += travelPay
		totDeductions += deductions
		totCampFees += campFees
		totEquipment += equipmentFees
		totMiles += miles
		totDues += dues
		totGames += numOfGames

		totExpenses = dues + equipmentFees + campFees + food
		grandTotExpenses += totExpenses

		netProfit = netRev - totExpenses

		totNetProfit += netProfit

		assignorFeesStr := utils.ConvertInt64ToAmtStr(assignorFees)
		travelPayStr := utils.ConvertInt64ToAmtStr(travelPay)
		totExpStr := utils.ConvertInt64ToAmtStr(totExpenses)
		gameFeesStr := utils.ConvertInt64ToAmtStr(gameFees)
		grossRevStr := utils.ConvertInt64ToAmtStr(grossRev)
		netRevStr := utils.ConvertInt64ToAmtStr(netRev)
		netProfitStr := utils.ConvertInt64ToAmtStr(netProfit)
		deductionsStr := utils.ConvertInt64ToAmtStr(deductions)
		numOfGamesStr := utils.ConvertInt64ToStr(numOfGames)

		rept = append(rept, fmt.Sprintf(reptFmtStr, a, numOfGamesStr, gameFeesStr, travelPayStr, grossRevStr, assignorFeesStr, deductionsStr, netRevStr, totExpStr, netProfitStr))

		dueStr := utils.ConvertInt64ToAmtStr(dues)
		campFeesStr := utils.ConvertInt64ToAmtStr(campFees)
		milesStr := utils.ConvertMilesToStr(miles)
		foodStr := utils.ConvertInt64ToAmtStr(food)
		equipmentStr := utils.ConvertInt64ToAmtStr(equipmentFees)

		totalExpenseStr := utils.ConvertInt64ToAmtStr(totExpenses)
		expenseRpt = append(expenseRpt, fmt.Sprintf(expReptFmtStr, a, dueStr, campFeesStr, milesStr, foodStr, equipmentStr, totalExpenseStr))

	}
	rept = append(rept, separator2)

	totGamesStr := utils.ConvertInt64ToStr(totGames)
	totGamesFeeStr := utils.ConvertInt64ToAmtStr(totGameFees)
	totTravelStr := utils.ConvertInt64ToAmtStr(totTravelPay)
	totGrossRevStr := utils.ConvertInt64ToAmtStr(totGrossRev)
	totAssignorFeeStr := utils.ConvertInt64ToAmtStr(totAssignorFees)
	totDeductionsStr := utils.ConvertInt64ToAmtStr(totDeductions)
	totNetRevStr := utils.ConvertInt64ToAmtStr(totNetRev)
	totExpenseStr := utils.ConvertInt64ToAmtStr(grandTotExpenses)
	totNetProfitStr := utils.ConvertInt64ToAmtStr(totNetProfit)

	rept = append(rept, fmt.Sprintf(reptFmtStr, "", totGamesStr, totGamesFeeStr, totTravelStr, totGrossRevStr, totAssignorFeeStr, totDeductionsStr, totNetRevStr, totExpenseStr, totNetProfitStr))
	rept = append(rept, "\n\n")
	expenseRpt = append(expenseRpt, expenseSeparator2)

	totDueStr := utils.ConvertInt64ToAmtStr(totDues)
	totCampFeesStr := utils.ConvertInt64ToAmtStr(totCampFees)
	totMilesStr := utils.ConvertMilesToStr(totMiles)
	totFoodStr := utils.ConvertInt64ToAmtStr(totFood)
	totEquipmentStr := utils.ConvertInt64ToAmtStr(totEquipment)
	grandTotalExpStr := utils.ConvertInt64ToAmtStr(grandTotExpenses)

	expenseRpt = append(expenseRpt, fmt.Sprintf(expReptFmtStr, "", totDueStr, totCampFeesStr, totMilesStr, totFoodStr, totEquipmentStr, grandTotalExpStr))

	rept = append(rept, expenseRpt...)

	return rept
}

func GenerateExpenseReport(records []model.ExpenseDescriptor) []string {

	fmt.Println("Generating Expense Report")
	rept := make([]string, 10, 20)
	title := "Expense Report\n"
	reptTimeMsg := getReportGeneratedDate()
	heading1 := "Expense ID              Date        Type              Mileage     Amount      Association     Game ID     Description\n"
	separator := "============================================================================================================================================\n"
	separator2 := "____________________________________________________________________________________________________________________________________________\n"
	reptFmtStr := "%-24s%-12s%-18s%-12s%-12s%-16s%-12s%-60s\n"

	maxLineLength := len(heading1)

	var expenses AssociationExpenses
	expenses.Init()

	newTitle := utils.CenterText(title, maxLineLength)
	newReptTimeMsg := utils.CenterText(reptTimeMsg, maxLineLength)
	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading1)
	rept = append(rept, separator)

	var totalNumOfExpenses int = 0
	var totalMiles int64 = 0
	var totalExpenses int64 = 0
	var amtStr string = ""
	var amt int64 = 0
	var err error
	var miles string = ""

	for _, rec := range records {

		amt, err = utils.ConvertAmtStrToInt64(rec.Amount)
		if err != nil {
			fmt.Println(err)
			continue
		}

		if rec.Type == "Mileage" {
			miles = TrimMileageStr(rec.Amount)
			milesInt64, err := utils.ConvertStrToInt64(miles)
			if err != nil {
				fmt.Println(err)
				continue
			}
			totalMiles += milesInt64
			amtStr = ""
		} else {
			amtStr = "$" + rec.Amount
			totalExpenses += amt
			miles = ""
		}

		expenses.UpdateExpense(rec.Association, rec.Type, amt)

		totalNumOfExpenses++
		rept = append(rept, fmt.Sprintf(reptFmtStr, rec.ExpenseId, rec.Date, rec.Type, miles, amtStr, rec.Association, rec.GameId, rec.Description))
	}

	totalLineReptFmt := "Total Expenses %-39d%-12d$%-11s\n\n"
	rept = append(rept, separator2)
	rept = append(rept, fmt.Sprintf(totalLineReptFmt, totalNumOfExpenses, totalMiles, utils.ConvertInt64ToAmtStr(totalExpenses)))

	rept = append(rept, "Break out Expenses by Association and Type\n\n")

	firstKeys := database.Associations
	secondKeys := database.ExpenseTypes

	reptLine := ""
	fmtStr := "      %-12s%-10s\n"
	for _, fk := range firstKeys {

		totExp := expenses.GetTotalExpensesByAssociation(fk)
		if totExp == 0 {
			continue
		}

		temp := expenses.GetExpenseByType(fk, "Mileage") / 100
		totMileage := strconv.FormatInt(temp, 10)

		reptLine = reptLine + "   Association: " + fk + " Total Expenses: $" + utils.ConvertInt64ToAmtStr(totExp) + " Total Mileage:" + totMileage + "\n"
		rept = append(rept, reptLine)
		reptLine = ""

		for _, sk := range secondKeys {
			expenseAmt := expenses.GetExpenseByType(fk, sk)
			line := ""
			if expenseAmt != 0 {
				if sk == "Mileage" {
					continue
				} else {
					expStr := "$" + utils.ConvertInt64ToAmtStr(expenseAmt)
					line = fmt.Sprintf(fmtStr, sk, expStr)
				}
				rept = append(rept, line)
				reptLine = ""
			}
		}
		rept = append(rept, "\n")
	}
	return rept

}

func GenerateGameReport(records []model.GameDescriptor) []string {

	fmt.Println("Generating Game Report")
	rept := make([]string, 10, 20)

	var grandTot int64
	var totalGames int64

	var ReportAssocGameTotals AssocGameTotalsMap
	ReportAssocGameTotals.Init()

	title := "Game Report\n"
	reptTimeMsg := getReportGeneratedDate()

	//                                                                                                                1         1         1         1         1         1         1
	//                    1         2         3         4         5         6         7         8         9         0         1         2         3         4         5         6
	//           1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890
	heading0 := "                                                                              Number                                                                                 \n"
	heading1 := "Game ID  Date             Time       Sport      Site                Field     Of Games    Level      Game Fee   Assoc     Status     Officials                         \n"
	separator := "======================================================================================================================================================================\n"
	reptFmtStr := "%-9s%-17s%-11s%-11s%-20s%-10s%-12s%-11s$%-10s%-10s%-11s%-37s\n"
	dateSeparator := "--------------------------------------------------------------------------------------------------------------------------------------------------------------------\n"

	maxLineLength := len(heading1)

	newTitle := utils.CenterText(title, maxLineLength)
	newReptTimeMsg := utils.CenterText(reptTimeMsg, maxLineLength)

	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading0)
	rept = append(rept, heading1)
	rept = append(rept, separator)

	prevDate := ""

	for _, rec := range records {

		if len(prevDate) == 0 {
			prevDate = rec.Date
		}

		if prevDate != rec.Date {
			rept = append(rept, dateSeparator)
			prevDate = rec.Date
		}
		numOfGames, _ := utils.ConvertStrToInt64(rec.NumOfGames)
		totalGames += numOfGames

		gameFee := CalculateGameFee(rec)
		ReportAssocGameTotals.Update(rec.Association, rec.Status, numOfGames, gameFee)

		grandTot += gameFee
		gameFeeStr := utils.ConvertInt64ToAmtStr(gameFee)

		dateStr := rec.Date + " (" + utils.DayOfWeekAbbreviation(rec.Date) + ")"
		officialStr := FormatOfficialString(rec.Referee, rec.U1, rec.U2)
		rept = append(rept, fmt.Sprintf(reptFmtStr, rec.GameId, dateStr, rec.Time, rec.Sport, rec.Site, rec.Field, rec.NumOfGames, rec.Level, gameFeeStr, rec.Association, rec.Status, officialStr))
	}

	grandTotalLine := "\n\nTotal Number of Games: %d  Total Game Fees: $%s\n"
	rept = append(rept, fmt.Sprintf(grandTotalLine, totalGames, utils.ConvertInt64ToAmtStr(grandTot)))

	rLines := ReportAssocGameTotals.FormatTotalLine()

	if len(rLines) > 1 {
		rept = append(rept, "\nBreakdown by Association and Status:\n")
		rept = append(rept, rLines...)
	}

	return rept

}
