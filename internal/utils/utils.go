package utils

import (
	"fmt"
	"ref-ledger-v2/internal/model"
	"strconv"
	"strings"
)

var UtilsVersion string = "ref-ledger-models-v2.1.0"

func CovertTextFileToJson(f string) error {

	return nil
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
