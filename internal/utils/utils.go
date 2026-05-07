package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"ref-ledger-v2/internal/model"
	"strconv"
	"strings"
	"time"
)

var UtilsVersion string = "ref-ledger-models-v2.1.0"
var layout string = "1/2/2006"

func getEndOfWeek(startOfWeek string) string {

	date, err := time.Parse(layout, startOfWeek)
	if err != nil {
		fmt.Println("Error parsing date")
		return ""
	}

	endOfWeek := date.AddDate(0, 0, 6)
	newDate := endOfWeek.Format(layout)
	return newDate

}

func getStartOfNextWeek() string {

	startOfThisWeek := getStartOfThisWeek()
	date, err := time.Parse(layout, startOfThisWeek)
	if err != nil {
		fmt.Println("Error parsing date")
		return ""
	}

	startOfNextWeek := date.AddDate(0, 0, 7)
	newDate := startOfNextWeek.Format(layout)
	return newDate

}

func getStartOfLastWeek() string {

	startOfThisWeek := getStartOfThisWeek()
	date, err := time.Parse(layout, startOfThisWeek)
	if err != nil {
		fmt.Println("Error parsing date")
		return ""
	}

	startOfLastWeek := date.AddDate(0, 0, -7)
	newDate := startOfLastWeek.Format(layout)
	return newDate
}

func getStartOfThisWeek() string {

	currentDate := time.Now()
	weekday := currentDate.Weekday()
	shift := int(weekday) % 7
	startOfWeek := currentDate.AddDate(0, 0, -shift)
	newDate := startOfWeek.Format(layout)

	return newDate
}

func getLastDayOfMonth(startOfMonth string) string {

	date, err := time.Parse(layout, startOfMonth)
	if err != nil {
		fmt.Println("Error parsing date")
		return ""
	}

	lastDay := date.AddDate(0, 1, 0).AddDate(0, 0, -1)
	return lastDay.Format(layout)
}

func getStartOfThisMonth() string {

	tempDate := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Now().Location())
	startOfThisMonth := tempDate.Format(layout)
	return startOfThisMonth
}

func getStartOfLastMonth() string {

	tempDate := time.Date(time.Now().Year(), time.Now().Month()-1, 1, 0, 0, 0, 0, time.Now().Location())
	startOfLastMonth := tempDate.Format(layout)
	return startOfLastMonth
}

func getStartOfNextMonth() string {

	tempDate := time.Date(time.Now().Year(), time.Now().Month()+1, 1, 0, 0, 0, 0, time.Now().Location())
	startOfLastMonth := tempDate.Format(layout)
	return startOfLastMonth
}

func FormatDateFilter(begin, end string) (string, string, error) {

	var bDate string
	var eDate string

	if begin == "today" {
		bDate = time.Now().Format(layout)
	} else if begin == "tomorrow" {
		bDate = time.Now().AddDate(0, 0, 1).Format(layout)
	} else if begin == "yesterday" {
		bDate = time.Now().AddDate(0, 0, -1).Format(layout)
	} else if begin == "this week" {
		bDate = getStartOfThisWeek()
	} else if begin == "next week" {
		bDate = getStartOfNextWeek()
	} else if begin == "last week" {
		bDate = getStartOfLastWeek()
	} else if begin == "this month" {
		bDate = getStartOfThisMonth()
	} else if begin == "next month" {
		bDate = getStartOfNextMonth()
	} else if begin == "last month" {
		bDate = getStartOfLastMonth()
	}

	if end == "" {
		eDate = bDate
	} else if end == "today" {
		eDate = time.Now().Format(layout)
	} else if end == "tomorrow" {
		eDate = time.Now().AddDate(0, 0, 1).Format(layout)
	} else if end == "yesterday" {
		eDate = time.Now().AddDate(0, 0, -1).Format(layout)
	} else if end == "this week" {
		eDate = getStartOfThisWeek()
	} else if end == "next week" {
		eDate = getStartOfNextWeek()
	} else if end == "last week" {
		eDate = getStartOfLastWeek()
	} else if end == "this month" {
		eDate = getStartOfThisMonth()
	} else if end == "next month" {
		eDate = getStartOfNextMonth()
	} else if end == "last month" {
		eDate = getStartOfLastMonth()
	}

	// Make sure the begin date is not later than the end date
	beginDate, err := time.Parse(layout, bDate)
	if err != nil {
		return "", "", err
	}

	endDate, err := time.Parse(layout, eDate)
	if err != nil {
		return "", "", err
	}

	if beginDate.After(endDate) {
		return "", "", fmt.Errorf("Begin date [%s] must not be later than end date [%s]", bDate, eDate)
	}
	return bDate, eDate, nil
}

func ParseInt64CSV(input string) ([]int64, error) {
	parts := strings.Split(input, ",")
	var result []int64

	for _, p := range parts {
		val := strings.TrimSpace(p)
		if val == "" {
			continue
		}

		num, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, err
		}
		result = append(result, num)
	}
	return result, nil
}

func ParseCsv(s string) []string {

	parts := strings.Split(s, ",")
	var result []string

	for _, p := range parts {
		val := strings.TrimSpace(p)
		if val != "" {
			result = append(result, val)
		}
	}
	return result
}

func DayOfWeekAbbreviation(date string) string {

	d, _ := time.Parse(layout, date)
	abbreviation := d.Format("Mon")
	return abbreviation
}

func ConvertGameFiltersToJsonFile(a, g, s, b, e string) (string, error) {

	var filters model.GameFilter
	var fileName string = "filters.json"

	assocValues := ParseCsv(a)
	gameIdValues, _ := ParseInt64CSV(g)
	statusValues := ParseCsv(s)

	filters.Status = statusValues
	filters.Association = assocValues
	filters.GameId = gameIdValues

	if b != "" || e != "" {
		filters.Date = &model.DateRange{}
	}

	if b != "" {
		filters.Date.From = b
	}

	if e != "" {
		filters.Date.To = e
	}

	// write JSON file
	file, err := os.Create(fileName)
	if err != nil {
		return "", fmt.Errorf("Failed to open %s", fileName)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(filters); err != nil {
		return "", fmt.Errorf("Failed to encode filters to file %s", fileName)
	}

	return fileName, nil
}

func CovertTextFileToJson(f string) error {

	return nil
}

func ConvertGameIdRangeToInt(g string) ([]int64, error) {

	var gameIds []int64

	rlist := strings.Split(g, "-")
	begin, err := strconv.ParseInt(rlist[0], 10, 64)
	if err != nil {
		fmt.Println("Failed to convert game id to int", rlist[0])
		return []int64{}, err
	}

	end, err := strconv.ParseInt(rlist[1], 10, 64)
	if err != nil {
		fmt.Println("Failed to convert game id to int", rlist[1])
		return []int64{}, err
	}

	if begin > end {
		return []int64{}, fmt.Errorf("Beginning game id[%d] must not be greater than ending game id [%d]", begin, end)
	}

	for i := begin; i <= end; i++ {
		gameIds = append(gameIds, i)
	}

	return gameIds, nil
}

func FormatGameIdStrSplice(gameIdStr string, maxLength int) []string {

	var gameIdSplice []string = []string{}
	var line string = ""

	gameIdList := strings.Split(gameIdStr, ";")
	for _, v := range gameIdList {

		if len(v)+len(line) >= maxLength {
			if line[len(line)-1] == ';' {
				line = line[:len(line)-1] + " "
			}
			gameIdSplice = append(gameIdSplice, line)
			line = ""
		}
		line += v + ";"
	}

	if len(line) > 0 {
		if line[len(line)-1] == ';' {
			line = line[:len(line)-1]
		}
		gameIdSplice = append(gameIdSplice, line)
	}

	return gameIdSplice
}

func ConvertGameIdsToRange(gameIds []int64) (string, int) {

	gameIdStr := ""

	if len(gameIds) == 1 {
		gameIdStr := fmt.Sprintf("%d", gameIds[0])
		return gameIdStr, 1
	}

	var beginGameId, endGameId int64
	beginGameId = 0
	endGameId = 0
	numOfGameIds := len(gameIds)

	for _, gameId := range gameIds {
		if beginGameId == 0 {
			beginGameId = gameId
		} else {
			if gameId == beginGameId+1 {
				endGameId = gameId
			} else {
				if beginGameId != 0 && endGameId != 0 {
					if gameId == endGameId+1 {
						endGameId = gameId
						continue
					}
					gameIdStr += fmt.Sprintf("%d-%d;", beginGameId, endGameId)
					beginGameId = gameId
					endGameId = 0
				} else {
					gameIdStr += fmt.Sprintf("%d;", beginGameId)
					beginGameId = gameId
					endGameId = 0
				}
			}
		}

	}

	if beginGameId != 0 && endGameId != 0 {
		gameIdStr += fmt.Sprintf("%d-%d", beginGameId, endGameId)
	} else if beginGameId != 0 {
		gameIdStr += fmt.Sprintf("%d", beginGameId)
	}

	return gameIdStr, numOfGameIds

}

func ConvertGameIdIntToStr(g []int64) (string, error) {

	var gameIdStr string

	if len(g) == 0 {
		return gameIdStr, nil
	}

	strSlice := make([]string, len(g))
	for i, v := range g {
		strSlice[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(strSlice, ","), nil

}

func ConvertGameIdStrToInt(g string) ([]int64, error) {

	var gameIds []int64

	glist := strings.Split(g, ";")

	for _, v := range glist {
		if strings.Contains(v, "-") {
			rangeOfIds, err := ConvertGameIdRangeToInt(v)
			if err != nil {
				fmt.Println("Failed to convert range of game ids to int[", v, "]")
				return []int64{}, err
			}
			gameIds = append(gameIds, rangeOfIds...)
		} else {
			gId, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				fmt.Println("Failed to convert single game id to int[", v, "]")
				return []int64{}, err
			}
			gameIds = append(gameIds, gId)
		}
	}

	return gameIds, nil
}

func ConvertInt64ToStr(num int64) string {

	str := strconv.FormatInt(num, 10) // Convert to string with base 10
	return str
}

func ConvertInt64ToAmtStr(amount int64) string {

	var dollars, cents int64

	cents = amount % 100
	dollars = amount / 100

	amtStr := fmt.Sprintf("%d.%02d", dollars, cents)

	return amtStr
}

func ConvertStrToInt64(s string) (int64, error) {

	var num int64

	_, err := fmt.Sscanf(s, "%d", &num)

	if err != nil {
		ErrStr := fmt.Sprintf("Error converting string %s to int64.  Reason: %s", s, err)
		fmt.Println(ErrStr)
		return int64(0), err
	}

	return num, nil
}

func ConvertAmtStrToInt64(s string) (int64, error) {

	var dollars, cents, amount int64

	_, err := fmt.Sscanf(s, "%d.%d", &dollars, &cents)

	if err != nil {
		ErrStr := fmt.Sprintf("Error converting string %s to int64.  Reason: %s", s, err)
		fmt.Println(ErrStr)
		return int64(0), err
	}

	amount = (dollars * 100) + cents

	return amount, nil

}

func ConvertGameDocToJson(doc []model.GameDoc, file string) error {

	// Convert to JSON
	jsonData, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	// Write to file
	err = os.WriteFile(file, jsonData, 0644)
	if err != nil {
		return err
	}
	return nil
}

func ConvertGameDocToGameDescr(doc model.GameDoc) model.GameDescriptor {

	gameId := ConvertInt64ToStr(doc.GameId)
	numOfGames := ConvertInt64ToStr(doc.NumOfGames)
	gameFee := ConvertInt64ToAmtStr(doc.GameFee)
	assignorFee := ConvertInt64ToAmtStr(doc.AssignorFee)
	travelPay := ConvertInt64ToAmtStr(doc.TravelPay)
	deductions := ConvertInt64ToAmtStr(doc.Deductions)

	gameDescr := model.GameDescriptor{
		GameId:      gameId,
		Date:        doc.Date,
		Time:        doc.Time,
		Sport:       doc.Sport,
		Site:        doc.Site,
		Field:       doc.Field,
		NumOfGames:  numOfGames,
		Level:       doc.Level,
		GameFee:     gameFee,
		TravelPay:   travelPay,
		AssignorFee: assignorFee,
		Deductions:  deductions,
		Association: doc.Association,
		Status:      doc.Status,
		Referee:     doc.Referee,
		U1:          doc.U1,
		U2:          doc.U2,
		ECO:         doc.ECO,
		Assignor:    doc.Assignor,
	}

	return gameDescr
}

func ConvertOfficialDescrToOfficialDoc(officialDescr model.OfficialDescriptor) model.OfficialDoc {

	doc := model.OfficialDoc{
		FirstName:   officialDescr.FirstName,
		LastName:    officialDescr.LastName,
		Phone:       officialDescr.Phone,
		Association: officialDescr.Association,
	}
	return doc
}

func ConvertPaymentDocToPaymentDescr(doc model.PaymentDoc) model.PaymentDescriptor {

	var gameIds string
	paymentAmt := ConvertInt64ToAmtStr(doc.PaymentAmt)
	gameIds, _ = ConvertGameIdsToRange(doc.GameIds)

	paymentDescr := model.PaymentDescriptor{
		PaymentId:   doc.PaymentId,
		PaymentDate: doc.PaymentDate,
		PaymentAmt:  paymentAmt,
		Association: doc.Association,
		GameIds:     gameIds,
	}

	return paymentDescr

}

func ConvertPaymentDescrToPaymentDoc(paymentDescr model.PaymentDescriptor) model.PaymentDoc {

	var gameIds []int64 = []int64{}
	gameIds, err := ConvertGameIdStrToInt(paymentDescr.GameIds)

	if err != nil {
		fmt.Println(err)
		return model.PaymentDoc{}
	}

	paymentAmt, _ := ConvertAmtStrToInt64(paymentDescr.PaymentAmt)

	doc := model.PaymentDoc{
		PaymentId:   paymentDescr.PaymentId,
		PaymentDate: paymentDescr.PaymentDate,
		PaymentAmt:  paymentAmt,
		Association: paymentDescr.Association,
		GameIds:     gameIds,
	}

	return doc
}

func ConvertGameDescrToGameDoc(gameDescr model.GameDescriptor) model.GameDoc {

	gameId, _ := ConvertStrToInt64(gameDescr.GameId)
	numOfGames, _ := ConvertStrToInt64(gameDescr.NumOfGames)
	gameFee, _ := ConvertAmtStrToInt64(gameDescr.GameFee)
	assignorFee, _ := ConvertAmtStrToInt64(gameDescr.AssignorFee)
	travelPay, _ := ConvertAmtStrToInt64(gameDescr.TravelPay)
	deductions, _ := ConvertAmtStrToInt64(gameDescr.Deductions)

	doc := model.GameDoc{
		GameId:      gameId,
		Date:        gameDescr.Date,
		Time:        gameDescr.Time,
		Sport:       gameDescr.Sport,
		Site:        gameDescr.Site,
		Field:       gameDescr.Field,
		NumOfGames:  numOfGames,
		Level:       gameDescr.Level,
		GameFee:     gameFee,
		TravelPay:   travelPay,
		AssignorFee: assignorFee,
		Deductions:  deductions,
		Association: gameDescr.Association,
		Status:      gameDescr.Status,
		Referee:     gameDescr.Referee,
		U1:          gameDescr.U1,
		U2:          gameDescr.U2,
		ECO:         gameDescr.ECO,
		Assignor:    gameDescr.Assignor,
	}

	return doc
}

func CenterText(text string, length int) string {

	midPoint := length / 2
	strLen := len(text)
	strMidPoint := strLen / 2
	newStr := ""

	if strLen >= length {
		return text
	}

	strFormat := strings.Replace("%-%ds%s", "%d", strconv.Itoa(midPoint-strMidPoint), -1)
	newStr = fmt.Sprintf(strFormat, " ", text)
	return newStr
}
