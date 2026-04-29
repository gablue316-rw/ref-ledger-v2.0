package reports

import (
	"fmt"
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

func GenerateGameReport(records []model.GameDescriptor) []string {

	fmt.Println("Generating Game Report")
	rept := make([]string, 10, 20)

	totals := make(map[string]map[string]int64)
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

	//dateSeparator := "--------------------------------------------------------------------------------------------------------------------------------------------------------------------\n"
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
		totals[fk] = make(map[string]int64)
		for _, sk := range secondKeys {
			totals[fk][sk] = 0

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
		totals[rec.Association][rec.Status] += gameFee

		gameFeeStr := utils.ConvertInt64ToAmtStr(gameFee)

		officialStr := formatOfficialString(rec.Referee, rec.U1, rec.U2)
		rept = append(rept, fmt.Sprintf(reptFmtStr, rec.GameId, rec.Date, rec.Time, rec.Sport, rec.Site, rec.Field, rec.NumOfGames, rec.Level, gameFeeStr, rec.Association, rec.Status, officialStr))
	}

	grandTotalLine := "\n\nTotal Number of Games: %d  Total Game Fees: $%s\n"
	rept = append(rept, fmt.Sprintf(grandTotalLine, totalGames, utils.ConvertInt64ToAmtStr(grandTot)))

	return rept

	/*
		rept := make([]string, 10, 20)
		var rec game
		var err error
		printAssocLine := true
		sqlQuery := ""

		logStr := fmt.Sprintf("Generating game report for association [%s] status [%s] begin date [%s] end date [%s] game ID [%s]", assoc, status, beginDate, endDate, gameId)
		logInfo(logStr)
		sqlQuery = "SELECT game_id, game_date, game_time, sport, level, site, field, status, association, num_games, game_fee_v2, travelPay_v2, referee, u1, u2, assignorFee_v2, deductions_v2 FROM games ORDER by game_date, game_id ASC"

		assocGameRpt := make(map[string]gameRpt)

		gameRptFilters := Filters{}

		if fname != "none" || lname != "none" {
			gameRptFilters.officialIdFilter, err = createOfficialIdSpliceFromName(db, fname, lname)
			if err != nil {
				return fmt.Errorf("Failed to create official id slice for Official %s %s Reason: %s", fname, lname, err.Error()), []string{}
			}

			//
			// If we didn't find any officials, then set officialIdFilter to 0.
			// This will prevent any game report records being shown
			//
			if len(gameRptFilters.officialIdFilter) == 0 {
				gameRptFilters.officialIdFilter = append(gameRptFilters.officialIdFilter, 0)
			}
		}

		if gameId != "all" {
			gameRptFilters.gameIdFilter, err = createGameIdSplice(gameId)
			if err != nil {
				return fmt.Errorf("failed to create game ID slice. Reason: %s", err.Error()), []string{}
			}
		}

		if status == "all" {
			gameRptFilters.statusFilter = []string{"Pending", "Paid", "Completed", "Cancelled"}
		} else {
			gameRptFilters.statusFilter = strings.Split(status, ",")
		}

		if assoc == "all" {
			err, gameRptFilters.assocFilter = getAllAssociations(db)
			if err != nil {
				return err, nil
			}
		} else {
			gameRptFilters.assocFilter = strings.Split(assoc, ",")
		}

		title := "Game Report\n"
		title2 := ""
		title3 := ""
		reptTimeMsg := getReportGeneratedDate()

		if assoc == "all" {
			title2 = "Association: All\n"
		} else {
			err, descr := getAssociationDescription(db, assoc)
			if err != nil {
				title2 = "Association: " + assoc + "\n"
			} else {
				title2 = "Association: " + descr + "\n"
			}
		}

		if assoc != "all" && status != "all" {
			title3 = fmt.Sprintf("Filtered by Association [%s] and Status [%s]\n", assoc, status)
		} else if assoc != "all" {
			title3 = fmt.Sprintf("Filtered by Association [%s]\n", assoc)
		} else if status != "all" {
			title3 = fmt.Sprintf("Filtered by Status [%s]\n", status)
		}

		//                                                                                                                1         1         1         1         1         1         1
		//                      1         2         3         4         5         6         7         8         9         0         1         2         3         4         5         6
		//             1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890
		heading0 := "                                                                              Number                                                                                 \n"
		heading1 := "Game ID  Date             Time       Sport      Site                Field     Of Games    Level      Game Fee   Assoc     Status     Officials                         \n"
		separator := "======================================================================================================================================================================\n"
		reptFmtStr := "%-9d%-17s%-11s%-11s%-20s%-10s%-12d%-11s$%-10s%-10s%-11s%-37s\n"

		dateSeparator := "--------------------------------------------------------------------------------------------------------------------------------------------------------------------\n"
		maxLineLength := len(heading1)

		grandTotalLine := "\n\nTotal Number of Games: %d  Total Game Fees: $%s\n"
		assocTotalLine := "    Association: %-12s Game Status: %-11s Total Number of Games: %-4d  Total Game Fees: $%s\n"

		newTitle := centerText(title, maxLineLength)
		newTitle2 := centerText(title2, maxLineLength)
		newReptTimeMsg := centerText(reptTimeMsg, maxLineLength)

		rept = append(rept, newTitle)
		rept = append(rept, newTitle2)
		if len(title3) != 0 {
			rept = append(rept, centerText(title3, maxLineLength))
		}

		rept = append(rept, newReptTimeMsg)
		rept = append(rept, heading0)
		rept = append(rept, heading1)
		rept = append(rept, separator)

		rows, err := db.Query(sqlQuery)

		if err != nil {
			return fmt.Errorf("sql error: table games: %w Reason: %s", ErrSqlQueryFailure, err.Error()), nil
		}

		defer rows.Close()

		prevDate := ""

		totalNumOfGames := 0
		calcGameFee := int64(0)
		totalGameFees := int64(0)

		//
		// Initialize assocGameRpt with status categories and zero values for game fees and
		// number of games
		//
		for _, v := range gameRptFilters.assocFilter {
			assocGameRpt[v] = gameRpt{
				gameFeesPerStatus: make(map[string]int64),
				gamesPerStatus:    make(map[string]int),
			}
			for _, s := range gameRptFilters.statusFilter {
				assocGameRpt[v].gameFeesPerStatus[s] = 0
				assocGameRpt[v].gamesPerStatus[s] = 0
			}
		}

		var dateValue time.Time
		var siteName string

		for rows.Next() {

			err = rows.Scan(&rec.game_id, &dateValue, &rec.time, &rec.sport, &rec.level, &rec.site, &rec.field, &rec.status, &rec.association, &rec.num_games, &rec.game_fee_int64, &rec.travelPay_int64, &rec.referee, &rec.u1, &rec.u2, &rec.assignorFee_int64, &rec.deductions_int64)
			if err != nil {
				return fmt.Errorf("sql error: table games: %w  Reason:%s", ErrSqlScanFailure, err.Error()), nil
			}

			err, siteName = getSiteName(db, rec.site)
			if err != nil {
				fmt.Println("Failed to get site name for site ID", rec.site, "Reason:", err.Error(), "Using site ID in report output instead of site name.")
				siteName = rec.site
			}

			rec.date = dateValue.Format("1/2/2006")

			//
			// Filter game records
			//

			if filterGameReport(gameRptFilters, rec) {
				continue
			}

			if beginDate != "none" && endDate == "none" {

				inRange, err := compareDates(rec.date, beginDate)
				if err != nil {
					return fmt.Errorf("date comparison error: %w", err), nil
				}
				if inRange < 0 {
					continue
				}
			} else if beginDate == "none" && endDate != "none" {

				inRange, err := compareDates(rec.date, endDate)
				if err != nil {
					return fmt.Errorf("date comparison error: %w", err), nil
				}
				if inRange > 0 {
					continue
				}
			} else if beginDate != "none" && endDate != "none" {

				inRangeBegin, err := compareDates(rec.date, beginDate)
				if err != nil {
					return fmt.Errorf("date comparison error: %w", err), nil
				}
				inRangeEnd, err := compareDates(rec.date, endDate)
				if err != nil {
					return fmt.Errorf("date comparison error: %w", err), nil
				}
				if inRangeBegin < 0 || inRangeEnd > 0 {
					continue
				}
			}

			if len(prevDate) == 0 {
				prevDate = rec.date
			}

			if prevDate != rec.date {
				rept = append(rept, dateSeparator)
				prevDate = rec.date
			}

			calcGameFee = rec.game_fee_int64*int64(rec.num_games) + rec.travelPay_int64 - rec.assignorFee_int64 - rec.deductions_int64
			calcGameFeeStr := convertInt64ToStr(calcGameFee)

			if err != nil {
				calcGameFeeStr = "CalcErr!"
			}

			assocGameRpt[rec.association].gameFeesPerStatus[rec.status] += calcGameFee
			assocGameRpt[rec.association].gamesPerStatus[rec.status] += rec.num_games

			officialsStr, err := formatOfficialString(db, rec.referee, rec.u1, rec.u2)
			if err != nil {
				fmt.Println("Failed to get official names.  Using default!")
				officialsStr = "unknown"
			}

			dateStr := rec.date + " (" + dayOfWeekAbbreviation(rec.date) + ")"
			rept = append(rept, fmt.Sprintf(reptFmtStr, rec.game_id, dateStr, rec.time, rec.sport, siteName, rec.field, rec.num_games, rec.level, calcGameFeeStr, rec.association, rec.status, officialsStr))
			totalNumOfGames += rec.num_games
			totalGameFees += calcGameFee
		}

		totalGameFeesStr := convertInt64ToStr(totalGameFees)
		rept = append(rept, fmt.Sprintf(grandTotalLine, totalNumOfGames, totalGameFeesStr))

		if len(gameRptFilters.assocFilter) < 2 && len(gameRptFilters.statusFilter) < 2 {
			printAssocLine = false
		}

		supplementalRept := make([]string, 10, 20)
		lines := 0
		if printAssocLine {
			supplementalRept = append(supplementalRept, ("\nBreakdown by Association and Status:\n"))
			for _, v := range gameRptFilters.assocFilter {
				for _, s := range gameRptFilters.statusFilter {
					if assocGameRpt[v].gamesPerStatus[s] > 0 {
						lines++
						gameFeesPerStatusStr := convertInt64ToStr(assocGameRpt[v].gameFeesPerStatus[s])
						if err != nil {
							gameFeesPerStatusStr = "CalcErr!"
						}
						supplementalRept = append(supplementalRept, fmt.Sprintf(assocTotalLine, v, s, assocGameRpt[v].gamesPerStatus[s], gameFeesPerStatusStr))
					}
				}
			}
		}

		if lines > 1 {
			rept = append(rept, supplementalRept...)
		}
		return nil, rept
	*/
}
