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

/*
type GameFilter struct {
	Status      []string `json:"status"`
	Association []string `json:"association"`
	GameId      []int64  `json:"gameId"`
}
*/

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

func ConvertGameFiltersToJsonFile(a, g, s string) error {

	var filters model.GameFilter

	assocValues := ParseCsv(a)
	gameIdValues, _ := ParseInt64CSV(g)
	statusValues := ParseCsv(s)

	filters.Status = statusValues
	filters.Association = assocValues
	filters.GameId = gameIdValues

	fmt.Println("Filter ready to be written to file", filters)

	// write JSON file
	file, err := os.Create("filters.json")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(filters); err != nil {
		panic(err)
	}

	return nil
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
				fmt.Println("Failed to single game id to int[", v, "]")
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
