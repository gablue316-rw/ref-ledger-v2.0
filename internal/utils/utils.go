package utils

import (
	"fmt"
	"ref-ledger-v2/internal/model"
)

var UtilsVersion string = "ref-ledger-models-v2.1.0"

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
		Status:      gameDescr.Status,
		Referee:     gameDescr.Referee,
		U1:          gameDescr.U1,
		U2:          gameDescr.U2,
		ECO:         gameDescr.ECO,
		Assignor:    gameDescr.Assignor,
	}

	return doc
}
