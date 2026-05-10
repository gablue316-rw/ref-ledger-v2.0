package reports

import (
	"context"
	"fmt"
	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strings"
	"time"
)

type ReportTotals struct {
	totalGames    int64
	totalGameFees int64
}

func getReportGeneratedDate() string {

	currentTime := time.Now()
	reportDate := currentTime.Format("01-02-2006")
	reportTime := currentTime.Format("15:04:05")
	reptTimeMsg := "Report generated on " + reportDate + " at " + reportTime + "\n\n"

	return reptTimeMsg
}

func formatOfficialString(ref, u1, u2 string) string {

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

func PrintReport(report []string) {

	for _, v := range report {
		fmt.Printf("%s", v)
	}

}

func calculateGameFee(gameRec model.GameDescriptor) int64 {

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

/*
func GeneratePaymentReport(records []model.PaymentDescriptor) []string {

	fmt.Println("Generating Payments Report")
	rept := make([]string, 10, 20)

	title := "Payment Report\n"
	reptTimeMsg := getReportGeneratedDate()

	totalPayments := 0
	totalDeposits := int64(0)

	//																									   			  1         1         1         1
	//                      1         2         3         4         5         6         7         8         9         0         1         2         3                                                                                                          1         1         1         1
	//           1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890
	heading1 := "Payment ID    Payment Date     Amount      Association     Game IDs     \n"
	separator := "==============================================================================================================================\n"
	reptFmtStr := "%-14s%-17s$%-11s%-16s%-60s\n"
	reptFmtStr2 := "%-59s%-60s\n"

	maxLineLength := len(heading1)

	newTitle := utils.centerText(title, maxLineLength)
	newReptTimeMsg := utils.centerText(reptTimeMsg, maxLineLength)

	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading1)
	rept = append(rept, separator)

	for _, rec := range records {

		rept = append(rept, fmt.Sprintf(reptFmtStr, rec.PaymentId, rec.PaymentDate, rec.PaymentAmt, rec.Association, rec.GameIds))

		totalPayments++
		totalDeposits += rec.payment_amount_int64

		rec.payment_date = dateValue.Format("1/2/2006")

		if len(rec.game_ids) > 60 {
			gameIdLines := formatGameIdStrSplice(rec.game_ids, 60)
			for i, v := range gameIdLines {
				if i == 0 {
					rept = append(rept, fmt.Sprintf(reptFmtStr, rec.payment_id, rec.payment_date, convertInt64ToStr(rec.payment_amount_int64), rec.association, v))
				} else {
					rept = append(rept, fmt.Sprintf(reptFmtStr2, "", v))
				}
			}
		} else {
			rept = append(rept, fmt.Sprintf(reptFmtStr, rec.payment_id, rec.payment_date, convertInt64ToStr(rec.payment_amount_int64), rec.association, rec.game_ids))
		}
	}
	if totalPayments > 0 {
		rept = append(rept, "\n")
		rept = append(rept, fmt.Sprintf("Total Payments: %d Total Deposits: $%s\n", totalPayments, convertInt64ToStr(totalDeposits)))
	}

	return nil, rept
}
*/

func GeneratePaymentReport(records []model.PaymentDescriptor) []string {

	fmt.Println("Generating Payment Report")
	rept := make([]string, 10, 20)
	var totalPayments int64 = 0
	var paymentAmtInt64 int64 = 0
	var totalNumOfPayments int = 0
	var err error

	reptFmtStr := "%-14s%-17s$%-11s%-16s%-60s\n"
	reptFmtStr2 := "%-59s%-60s\n"

	title := "Game Report\n"
	heading1 := "Payment ID    Payment Date     Amount      Association     Game IDs\n"
	separator := "==============================================================================================================================\n"
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
			fmt.Println(err)
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
		gameRecords, err := database.QueryAggregatedGames(parentCtx, "refLedger_v2", "games", gFilters)

		if err != nil {
			fmt.Println("Error:", err)
			return nil
		}

		gameIds := []int64{}
		acctsRecv := int64(0)

		for _, r := range gameRecords {

			g, err := utils.ConvertStrToInt64(r.GameId)
			totalGameId++

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

func GenerateGameReport(records []model.GameDescriptor) []string {

	fmt.Println("Generating Game Report")
	rept := make([]string, 10, 20)

	totals := make(map[string]map[string]*ReportTotals)
	var grandTot int64
	var totalGames int64

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

	//grandTotalLine := "\n\nTotal Number of Games: %d  Total Game Fees: $%s\n"
	//assocTotalLine := "    Association: %-12s Game Status: %-11s Total Number of Games: %-4d  Total Game Fees: $%s\n"

	newTitle := utils.CenterText(title, maxLineLength)
	newReptTimeMsg := utils.CenterText(reptTimeMsg, maxLineLength)

	rept = append(rept, newTitle)
	rept = append(rept, newReptTimeMsg)
	rept = append(rept, heading0)
	rept = append(rept, heading1)
	rept = append(rept, separator)

	//
	// Initialize totals map
	//
	firstKeys := database.Associations
	secondKeys := database.PermittedGameStatusValues

	for _, fk := range firstKeys {
		totals[fk] = make(map[string]*ReportTotals)
		for _, sk := range secondKeys {
			totals[fk][sk] = &ReportTotals{}

		}
	}

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

		gameFee := calculateGameFee(rec)
		grandTot += gameFee
		if totals[rec.Association] == nil {
			fmt.Println("totals[", rec.Association, "] is equal to nil")
			continue
		}

		if totals[rec.Association][rec.Status] == nil {
			fmt.Println("totals[", rec.Association, "][", rec.Status, "] is equal to nil")
			fmt.Println("Game Id: ", rec.GameId)
			continue
		}
		totals[rec.Association][rec.Status].totalGameFees += gameFee
		totals[rec.Association][rec.Status].totalGames += numOfGames

		gameFeeStr := utils.ConvertInt64ToAmtStr(gameFee)

		dateStr := rec.Date + " (" + utils.DayOfWeekAbbreviation(rec.Date) + ")"
		officialStr := formatOfficialString(rec.Referee, rec.U1, rec.U2)
		rept = append(rept, fmt.Sprintf(reptFmtStr, rec.GameId, dateStr, rec.Time, rec.Sport, rec.Site, rec.Field, rec.NumOfGames, rec.Level, gameFeeStr, rec.Association, rec.Status, officialStr))
	}

	grandTotalLine := "\n\nTotal Number of Games: %d  Total Game Fees: $%s\n"
	rept = append(rept, fmt.Sprintf(grandTotalLine, totalGames, utils.ConvertInt64ToAmtStr(grandTot)))

	supplementalRept := []string{}
	lines := 0
	supplementalRept = append(supplementalRept, ("\nBreakdown by Association and Status:\n"))
	assocTotalLine := "    Association: %-12s Game Status: %-11s Total Number of Games: %-4d  Total Game Fees: $%s\n"

	for fk, inner := range totals {
		for sk, counters := range inner {
			if counters.totalGames == 0 {
				continue
			}
			lines++
			supplementalRept = append(supplementalRept, fmt.Sprintf(assocTotalLine, fk, sk, counters.totalGames, utils.ConvertInt64ToAmtStr(counters.totalGameFees)))
		}
	}

	if lines > 1 {
		rept = append(rept, supplementalRept...)
	}

	return rept

}
