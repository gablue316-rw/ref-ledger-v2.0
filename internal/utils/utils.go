package utils

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"ref-ledger-v2/internal/model"
	"strconv"
	"strings"
	"time"
)

var UtilsVersion string = "ref-ledger-models-v2.1.0"
var layout string = "1/2/2006"

/* Log file paths for standalone and containerized deployments */
/* Use standalone when running Ref Ledger outside of a Docker container, and use container when running Ref Ledger inside a Docker container. */

/* Check environment_variables.txt for the environment variables that must be set for Ref Ledger to run properly. */
var logFileFullPathName string = ""

var AuditLog *log.Logger = nil

const (
	MinTaxYear = 2000
	MaxTaxYear = 2100
)

var ErrInvalidTaxYear = fmt.Errorf(
	"invalid tax year; expected a four-digit year between %d and %d",
	MinTaxYear,
	MaxTaxYear,
)

func copyGameOfficials(
	officials []model.GameOfficialDoc,
) []model.GameOfficialDoc {
	if officials == nil {
		return nil
	}

	result := make([]model.GameOfficialDoc, len(officials))
	copy(result, officials)
	return result
}

func GetLogFilePath() string {

	fmt.Println("Getting log file path from environment variable LOG_FILE_PATH...")
	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "./logs/ref-ledgerV2-webServer.log"
	}

	fmt.Println("Log file path:", logFilePath)
	return logFilePath
}

func InitLogging() error {

	if AuditLog != nil {
		return nil
	}

	logFileFullPathName = GetLogFilePath()

	logFile, err := os.OpenFile(
		logFileFullPathName,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)

	if err != nil {
		return err
	}

	AuditLog = log.New(
		logFile,
		"FILE: ",
		log.LstdFlags|log.Lshortfile,
	)

	return nil

}

func FormatDateFilter(begin, end string) (string, string, error) {
	const (
		inputLayout  = "2006-01-02"
		outputLayout = "1/2/2006"
	)

	var beginDate time.Time
	var endDate time.Time
	var err error

	if begin != "" {
		beginDate, err = time.Parse(inputLayout, begin)
		if err != nil {
			return "", "", fmt.Errorf(
				"invalid begin date %q; expected YYYY-MM-DD: %w",
				begin,
				err,
			)
		}
	}

	if end != "" {
		endDate, err = time.Parse(inputLayout, end)
		if err != nil {
			return "", "", fmt.Errorf(
				"invalid end date %q; expected YYYY-MM-DD: %w",
				end,
				err,
			)
		}
	}

	if !beginDate.IsZero() &&
		!endDate.IsZero() &&
		beginDate.After(endDate) {
		return "", "", fmt.Errorf(
			"begin date [%s] must not be later than end date [%s]",
			begin,
			end,
		)
	}

	var formattedBegin string
	var formattedEnd string

	if !beginDate.IsZero() {
		formattedBegin = beginDate.Format(outputLayout)
	}

	if !endDate.IsZero() {
		formattedEnd = endDate.Format(outputLayout)
	}

	return formattedBegin, formattedEnd, nil
}

func GenerateRandomString(length int) string {

	b := make([]byte, length)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)

}

func DayOfWeekAbbreviation(date string) string {

	d, _ := time.Parse(layout, date)
	abbreviation := d.Format("Mon")
	return abbreviation
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

func ConvertGameIdStrToInt(g string) ([]int64, error) {

	var gameIds []int64

	glist := strings.Split(g, ";")

	for _, v := range glist {
		if strings.Contains(v, "-") {
			rangeOfIds, err := ConvertGameIdRangeToInt(v)
			if err != nil {
				AuditLog.Printf("Failed to convert range of game ids to int for game id string [%s].  Reason: %v", v, err)
				fmt.Println("Failed to convert range of game ids to int[", v, "]")
				return []int64{}, err
			}
			gameIds = append(gameIds, rangeOfIds...)
		} else {
			gId, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				fmt.Println("Failed to convert single game id to int[", v, "]")
				AuditLog.Printf("Failed to convert single game id to int for game id string [%s].  Reason: %v", v, err)
				return []int64{}, err
			}
			gameIds = append(gameIds, gId)
		}
	}

	return gameIds, nil
}

func ConvertStrMilesToInt64(miles string) (int64, error) {

	var num int64

	if miles == "" {
		return int64(0), nil
	}
	_, err := fmt.Sscanf(miles, "%d", &num)

	if err != nil {
		ErrStr := fmt.Sprintf("Error converting string %s to int64.  Reason: %s", miles, err)
		fmt.Println(ErrStr)
		return int64(0), err
	}
	num = num * 100

	return num, nil

}
func ConvertMilesToStr(miles int64) string {

	str := strconv.FormatInt((miles / 100), 10) // Convert to string with base 10
	return str
}

func ConvertIntToStr(num int) string {

	str := strconv.Itoa(num)
	return str
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

	if s == "" {
		return int64(0), nil
	}
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

	s = strings.ReplaceAll(s, "$", "")

	if s == "" {
		return int64(0), nil
	}

	if !strings.Contains(s, ".") {
		s = s + ".00"
	}

	_, err := fmt.Sscanf(s, "%d.%d", &dollars, &cents)

	if err != nil {
		ErrStr := fmt.Sprintf("Error converting string %s to int64.  Reason: %s", s, err)
		fmt.Println(ErrStr)
		return int64(0), err
	}

	amount = (dollars * 100) + cents

	return amount, nil

}

func ConvertGameDocToGameDescr(doc model.GameDoc) model.GameDescriptor {

	gameId := ConvertInt64ToStr(doc.GameId)
	numOfGames := ConvertInt64ToStr(doc.NumOfGames)
	gameFee := ConvertInt64ToAmtStr(doc.GameFee)
	assignorFee := ConvertInt64ToAmtStr(doc.AssignorFee)
	travelPay := ConvertInt64ToAmtStr(doc.TravelPay)
	deductions := ConvertInt64ToAmtStr(doc.Deductions)

	gameDescr := model.GameDescriptor{
		Version:     doc.Version,
		GameId:      gameId,
		Date:        doc.Date,
		Time:        doc.Time,
		Sport:       doc.Sport,
		Site:        doc.Site,
		Field:       doc.Field,
		NumOfGames:  numOfGames,
		Level:       doc.Level,
		Home:        doc.Home,
		Visitor:     doc.Visitor,
		GameFee:     gameFee,
		TravelPay:   travelPay,
		AssignorFee: assignorFee,
		Deductions:  deductions,
		Association: doc.Association,
		Status:      doc.Status,
		Officials:   copyGameOfficials(doc.Officials),
		Assignor:    doc.Assignor,
		TaxYear:     doc.TaxYear,
		Notes:       doc.Notes,
	}

	return gameDescr
}

func ConvertPaymentDocToPaymentDescr(doc model.PaymentDoc) model.PaymentDescriptor {
	paymentAmt := ConvertInt64ToAmtStr(doc.PaymentAmt)
	gameIds, _ := ConvertGameIdsToRange(doc.GameIds)

	var taxYear string
	if doc.TaxYear != 0 {
		taxYear = strconv.Itoa(doc.TaxYear)
	}

	return model.PaymentDescriptor{
		Version:     doc.Version,
		PaymentId:   doc.PaymentId,
		PaymentDate: doc.PaymentDate,
		PaymentAmt:  paymentAmt,
		Association: doc.Association,
		GameIds:     gameIds,
		TaxYear:     taxYear,
	}
}

func ConvertPaymentDescrToPaymentDoc(paymentDescr model.PaymentDescriptor) model.PaymentDoc {
	gameIds, err := ConvertGameIdStrToInt(paymentDescr.GameIds)
	if err != nil {
		fmt.Println(err)
		return model.PaymentDoc{}
	}

	paymentAmt, _ := ConvertAmtStrToInt64(paymentDescr.PaymentAmt)

	var taxYear int
	if paymentDescr.TaxYear != "" {
		taxYear, err = strconv.Atoi(paymentDescr.TaxYear)
		if err != nil {
			fmt.Println("Invalid tax year:", err)
			return model.PaymentDoc{}
		}
	}

	return model.PaymentDoc{
		PaymentId:   paymentDescr.PaymentId,
		PaymentDate: paymentDescr.PaymentDate,
		PaymentAmt:  paymentAmt,
		Association: paymentDescr.Association,
		GameIds:     gameIds,
		TaxYear:     taxYear,
	}
}

func CalculateGameFee(gameRec model.GameDescriptor) int64 {

	var gameFee int64
	var gFee int64
	var numOfGames int64
	var travelPay int64
	var deductions int64
	var assignorFee int64

	gFee, _ = ConvertAmtStrToInt64(gameRec.GameFee)
	numOfGames, _ = ConvertStrToInt64(gameRec.NumOfGames)
	travelPay, _ = ConvertAmtStrToInt64(gameRec.TravelPay)
	deductions, _ = ConvertAmtStrToInt64(gameRec.Deductions)
	assignorFee, _ = ConvertAmtStrToInt64(gameRec.AssignorFee)

	gameFee = gFee*numOfGames + travelPay - deductions - assignorFee

	return gameFee
}

func ConvertExpenseDocToExpenseDescr(doc model.ExpenseDoc) model.ExpenseDescriptor {

	expenseAmt := ConvertInt64ToAmtStr(doc.Amount)
	gameId := ConvertInt64ToStr(doc.GameId)
	mileage := ConvertMilesToStr(doc.Mileage)
	descr := model.ExpenseDescriptor{
		Version:     doc.Version,
		ExpenseId:   doc.ExpenseId,
		Date:        doc.Date,
		Type:        doc.Type,
		Amount:      expenseAmt,
		Mileage:     mileage,
		Association: doc.Association,
		GameId:      gameId,
		Description: doc.Description,
		TaxYear:     doc.TaxYear,
	}
	return descr
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
		Home:        gameDescr.Home,
		Visitor:     gameDescr.Visitor,
		NumOfGames:  numOfGames,
		Level:       gameDescr.Level,
		GameFee:     gameFee,
		TravelPay:   travelPay,
		AssignorFee: assignorFee,
		Deductions:  deductions,
		Association: gameDescr.Association,
		Status:      gameDescr.Status,
		Officials:   copyGameOfficials(gameDescr.Officials),
		Assignor:    gameDescr.Assignor,
		TaxYear:     gameDescr.TaxYear,
		Notes:       gameDescr.Notes,
	}

	return doc
}

func ConvertDateStringToTime(
	dateValue string,
	timeValue string,
) (time.Time, error) {

	dateValue = strings.TrimSpace(dateValue)
	timeValue = strings.TrimSpace(timeValue)

	if dateValue == "" {
		return time.Time{}, fmt.Errorf("game date is required")
	}

	if timeValue == "" {
		return time.Time{}, fmt.Errorf("game time is required")
	}

	dateTimeText := dateValue + " " + timeValue

	gameDateTime, err := time.ParseInLocation(
		"1/2/2006 3:04 PM",
		dateTimeText,
		time.Local,
	)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"unable to parse game date and time %q: %w",
			dateTimeText,
			err,
		)
	}

	return gameDateTime, nil
}
