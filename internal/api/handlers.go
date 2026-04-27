package api

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"strings"
)

var ApiVersion string = "ref-ledger-api-v2.1.0"

/*
func DelGame(game model.GameDescriptor) {

    ctx := database.GetContext()
	fmt.Println("Deleting game with Game Id:", game.GameId)
	doc := model.GameDoc{
		GameId: game.GameId,
	}

	database.DeleteOneDoc(ctx, doc, "refLedger_v2", "games")

}
*/

func AddGames(parentCtx context.Context, game []model.GameDescriptor) {

	database.InsertDocs(parentCtx, game, database.Database, "games")
}

func DelGameTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "games")

}

func DumpGames(parentCtx context.Context) {

	database.DumpCollection(parentCtx, database.Database, "games")

}

func BulkAddGames(parentCtx context.Context, file string) {

	fmt.Println("Peforming Bulk Update for Games Collection")

	games := []model.GameDescriptor{}
	// Read file
	fd, err := os.Open(file)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer fd.Close()

	sc := bufio.NewScanner(fd)

	for sc.Scan() {

		line := sc.Text()
		fields := strings.Split(line, ",")

		game := model.GameDescriptor{
			GameId:      fields[2],
			Date:        fields[0],
			Time:        fields[1],
			Sport:       fields[3],
			Site:        fields[5],
			Field:       fields[6],
			NumOfGames:  fields[7],
			Level:       fields[4],
			GameFee:     fields[8],
			TravelPay:   fields[18],
			AssignorFee: fields[17],
			Deductions:  fields[9],
			Association: fields[11],
			Status:      fields[10],
			Referee:     fields[12],
			U1:          fields[13],
			U2:          fields[14],
			ECO:         fields[15],
			Assignor:    fields[16],
		}
		games = append(games, game)

	}

	AddGames(parentCtx, games)
}
