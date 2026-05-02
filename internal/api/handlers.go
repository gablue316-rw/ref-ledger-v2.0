package api

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
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

func RebuildTable(parentCtx context.Context, table, file string) error {

	fmt.Println("Rebuilding table", table)

	if table == "games" {
		DelGamesTable(parentCtx)
		BulkAddGames(parentCtx, file)
	} else if table == "officials" {
		DelOfficialsTable(parentCtx)
		BulkAddOfficials(parentCtx, file)
	} else {
		return fmt.Errorf("Invalid table")
	}

	return nil
}

func UpdateGame(parentCtx context.Context, cmd string, gameIds []int64) error {

	var field string
	var value string
	var cmdList []string

	cmdList = strings.Split(cmd, ":")
	field = cmdList[0]
	value = cmdList[1]
	var update bson.M

	filter := bson.M{
		"gameId": bson.M{
			"$in": gameIds,
		},
	}

	switch field {
	case "Status":
		update = bson.M{
			"$set": bson.M{
				"status": value,
			},
		}
	case "U1":
		update = bson.M{
			"$set": bson.M{
				"u1": value,
			},
		}
	default:
		return fmt.Errorf("%s not supported", field)

	}

	if len(gameIds) > 1 {
		database.UpdateManyDoc(parentCtx, filter, update, database.Database, "games")
	} else {
		database.UpdateOneDoc(parentCtx, filter, update, database.Database, "games")
	}

	return nil
}

func AddGames(parentCtx context.Context, game []model.GameDescriptor) {

	database.InsertGameDocs(parentCtx, game, database.Database, "games")
}

func AddOfficials(parentCtx context.Context, official []model.OfficialDescriptor) {

	database.InsertOfficialDocs(parentCtx, official, database.Database, "officials")
}

func DelGamesTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "games")

}

func DelOfficialsTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "officials")

}

func DumpGames(parentCtx context.Context) {

	database.DumpGamesCollection(parentCtx, database.Database, "games")

}

func DumpOfficials(parentCtx context.Context) {

	database.DumpOfficialsCollection(parentCtx, database.Database, "officials")

}

func DumpTable(parentCtx context.Context, table string) {

	switch table {
	case "games":
		DumpGames(parentCtx)
	case "officials":
		DumpOfficials(parentCtx)
	default:
		fmt.Println("Table", table, "not supported")
	}

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

func BulkAddOfficials(parentCtx context.Context, file string) {

	fmt.Println("Peforming Bulk Update for Officials Collection")

	officials := []model.OfficialDescriptor{}
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

		official := model.OfficialDescriptor{
			FirstName:   fields[0],
			LastName:    fields[1],
			Phone:       fields[2],
			Association: fields[3],
		}
		officials = append(officials, official)

	}

	AddOfficials(parentCtx, officials)
}
