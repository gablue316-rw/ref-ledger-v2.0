package api

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"strconv"
	"strings"

	"encoding/base64"
	"encoding/json"

	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
)

var ApiVersion string = "ref-ledger-api-v2.1.0"

func UseDefaultConfig() model.Config {

	fmt.Println("Using Default Configuration")

	config := model.Config{
		AppName:  "RefLedger",
		Version:  "2.0.0",
		UserName: "cmVmTGVkZ2VyMzE2QGdtYWlsLmNvbQ==",
		Password: "dHpsZ3l0am5zZmNzdnhiaA==",
		Features: model.FeaturesConfig{
			EnableDeleteOnCancel: true,
			LogFile:              "refLedger.log",
			DbUpdateLog:          "dbUpdate.log",
		},
	}

	return config

}

func processConfigFile(configFile string) error {

	var config model.Config

	fmt.Println("Processing json config file", configFile)

	fd, err := os.OpenFile(configFile, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return fmt.Errorf("Error opening %s file: %v", configFile, err)
	}

	defer fd.Close()

	if err = json.NewDecoder(fd).Decode(&config); err != nil {
		return fmt.Errorf("failed to decode %s: %w", configFile, err)
	}

	return nil
}

// Silently fail.  We don't want anyone to know we are decrypting the email credentials
func DecryptEmailCredentials(userName, password string) (string, string) {

	var decryptedUserName []byte = []byte{}
	var decryptedPwd []byte = []byte{}

	//
	// Decrypt User Name
	//
	decryptedUserName, err := base64.StdEncoding.DecodeString(userName)
	if err != nil {
		return "", ""
	}

	//
	// Decrypt Password
	//
	decryptedPwd, err = base64.StdEncoding.DecodeString(password)

	if err != nil {
		return "", ""
	}

	return string(decryptedUserName), string(decryptedPwd)

}

func DelGame(parentCtx context.Context, game model.GameDescriptor) {

	fmt.Println("Deleting game with Game Id:", game.GameId)
	gameId, _ := utils.ConvertStrToInt64(game.GameId)
	filter := bson.M{"gameId": gameId}

	database.DeleteOneDoc(parentCtx, filter, "refLedger_v2", "games")

}

func UpdatePayments(parentCtx context.Context, file string) error {

	fmt.Println("Adding to Payments Collection")

	payments := []model.PaymentDescriptor{}

	// Read file
	fd, err := os.Open(file)
	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("Failed to open file %s.  Reason: %s", file, err)
	}
	defer fd.Close()

	sc := bufio.NewScanner(fd)

	recordsRead := 0
	recordsAppended := 0
	recordsDeleted := 0
	validationErrors := 0

	type PaymentDescriptor struct {
		PaymentId   string
		PaymentDate string
		PaymentAmt  string
		Association string
		GameIds     string
	}
	for sc.Scan() {

		line := sc.Text()
		recordsRead++
		fields := strings.Split(line, ",")

		payment := model.PaymentDescriptor{
			PaymentId:   fields[0],
			PaymentDate: fields[1],
			PaymentAmt:  fields[2],
			Association: fields[3],
			GameIds:     fields[4],
		}

		err = ValidatePaymentDescriptor(parentCtx, payment)
		if err != nil {
			fmt.Println(err)
			validationErrors++
			continue
		}

		payments = append(payments, payment)

		recordsAppended++
	}

	fmt.Println("Records Read", recordsRead, "Records Deleted", recordsDeleted, "Records Appended", recordsAppended, "Validation Errors", validationErrors)
	AddPayments(parentCtx, payments)

	return nil
}

func UpdateGames(parentCtx context.Context, file string) error {

	fmt.Println("Adding to Games Collection")

	games := []model.GameDescriptor{}
	// Read file
	fd, err := os.Open(file)
	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("Failed to open file %s.  Reason: %s", file, err)
	}
	defer fd.Close()

	sc := bufio.NewScanner(fd)

	recordsRead := 0
	recordsAppended := 0
	recordsDeleted := 0
	validationErrors := 0

	for sc.Scan() {

		line := sc.Text()
		recordsRead++
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

		if game.Status == "Delete" {
			DelGame(parentCtx, game)
			recordsDeleted++
		}

		err = ValidateGameDescriptor(parentCtx, game)
		if err != nil {
			fmt.Println(err)
			validationErrors++
			continue
		}

		games = append(games, game)

		recordsAppended++
	}

	fmt.Println("Records Read", recordsRead, "Records Deleted", recordsDeleted, "Records Appended", recordsAppended, "Validation Errors", validationErrors)
	AddGames(parentCtx, games)

	return nil
}

// Stubbed out for now
func ValidatePaymentDescriptor(parentCtx context.Context, p model.PaymentDescriptor) error {
	return nil
}

func ValidateGameDescriptor(parentCtx context.Context, g model.GameDescriptor) error {

	var errorFormat string = "%s %s not found!  Record Dropped!"

	if g.Referee != "Unassigned" {
		results, err := database.FindOfficial(parentCtx, g.Referee)
		if !results || err != nil {
			return fmt.Errorf(errorFormat, "Referee", g.Referee)
		}
	}
	if g.U1 != "Unassigned" {
		results, err := database.FindOfficial(parentCtx, g.U1)
		if !results || err != nil {
			return fmt.Errorf(errorFormat, "U1", g.U1)
		}
	}
	if g.U2 != "Unassigned" {
		results, err := database.FindOfficial(parentCtx, g.U2)
		if !results || err != nil {
			return fmt.Errorf(errorFormat, "U2", g.U2)
		}
	}
	if g.ECO != "Unassigned" {
		results, err := database.FindOfficial(parentCtx, g.ECO)
		if !results || err != nil {
			return fmt.Errorf(errorFormat, "ECO", g.ECO)
		}
	}
	if g.Assignor != "Unassigned" {
		results, err := database.FindOfficial(parentCtx, g.Assignor)
		if !results || err != nil {
			return fmt.Errorf(errorFormat, "Assignor", g.Assignor)
		}
	}
	return nil
}

func RebuildTable(parentCtx context.Context, table, file string) error {

	fmt.Println("Rebuilding table", table)

	switch table {
	case "games":
		DelGamesTable(parentCtx)
		BulkAddGames(parentCtx, file)
	case "officials":
		DelOfficialsTable(parentCtx)
		BulkAddOfficials(parentCtx, file)
	case "payments":
		DelPaymentsTable(parentCtx)
		BulkAddPayments(parentCtx, file)
	default:
		return fmt.Errorf("Invalid table")
	}

	return nil
}

func UpdatePayment(parentCtx context.Context) {

}

func UpdateGame(parentCtx context.Context, cmd string, gameIds []int64) error {

	var field string
	var value string
	var cmdList []string
	var int64Fields []string = []string{"numOfGames", "gameFee", "travelPay", "assignorFee", "deductions"}
	var officialFields []string = []string{"referee", "u1", "u2", "eco", "assignor"}

	cmdList = strings.Split(cmd, ";")

	for _, command := range cmdList {

		parts := strings.Split(command, ":")

		field = parts[0]
		value = parts[1]
		var update bson.M

		//
		// Time changes will look like the following:
		//
		//    Time:6:15 PM
		//
		// The above strings.Split command will split the cmd into 3 elements.
		// So we will need to reassemble the time
		//
		if len(command) == 3 {
			value = cmdList[1] + ":" + cmdList[2]
		}

		filter := bson.M{
			"gameId": bson.M{
				"$in": gameIds,
			},
		}

		if field == "status" && value == "Delete" {
			database.DeleteOneDoc(parentCtx, filter, database.Database, "games")
			return nil
		}

		exists := slices.Contains(officialFields, field)
		if exists {
			found, err := database.FindOfficial(parentCtx, value)
			if !found || err != nil {
				return fmt.Errorf("Failed to find %s %s.  Reason: %s", field, value, err)
			}
		}

		exists = slices.Contains(int64Fields, field)
		if exists {
			valueInt64, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return fmt.Errorf("Error: %s", err)
			}
			valueInt64 = valueInt64 * 100
			update = bson.M{
				"$set": bson.M{
					field: valueInt64,
				},
			}
		} else {
			update = bson.M{
				"$set": bson.M{
					field: value,
				},
			}
		}

		if len(gameIds) > 1 {
			database.UpdateManyDoc(parentCtx, filter, update, database.Database, "games")
		} else {
			err := database.UpdateOneDoc(parentCtx, filter, update, database.Database, "games")
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	return nil

}

func AddPayments(parentCtx context.Context, payment []model.PaymentDescriptor) {

	database.InsertPaymentDocs(parentCtx, payment, database.Database, "payments")
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

func DelPaymentsTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "payments")

}

func DumpGames(parentCtx context.Context) {

	database.DumpGamesCollection(parentCtx, database.Database, "games")

}

func DumpOfficials(parentCtx context.Context) {

	database.DumpOfficialsCollection(parentCtx, database.Database, "officials")

}

func DumpPayments(parentCtx context.Context) {

	database.DumpPaymentsCollection(parentCtx, database.Database, "payments")

}

func DumpTable(parentCtx context.Context, table string) {

	switch table {
	case "games":
		DumpGames(parentCtx)
	case "officials":
		DumpOfficials(parentCtx)
	case "payments":
		DumpPayments(parentCtx)
	default:
		fmt.Println("Table", table, "not supported")
	}

}

func BulkAddPayments(parentCtx context.Context, file string) {

	fmt.Println("Peforming Bulk Update for Payments Collection")

	payments := []model.PaymentDescriptor{}
	// Read file
	fd, err := os.Open(file)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer fd.Close()

	sc := bufio.NewScanner(fd)

	recordsRead := 0
	recordsAppended := 0
	validationErrors := 0

	for sc.Scan() {

		line := sc.Text()
		recordsRead++
		fields := strings.Split(line, ",")

		payment := model.PaymentDescriptor{
			PaymentId:   fields[0],
			PaymentDate: fields[1],
			PaymentAmt:  fields[2],
			Association: fields[3],
			GameIds:     fields[4],
		}

		err = ValidatePaymentDescriptor(parentCtx, payment)
		if err != nil {
			fmt.Println(err)
			validationErrors++
			continue
		}

		payments = append(payments, payment)

		recordsAppended++
	}

	fmt.Println("Records Read", recordsRead, "Records Appended", recordsAppended, "Validation Errors", validationErrors)
	AddPayments(parentCtx, payments)
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

	recordsRead := 0
	recordsAppended := 0
	validationErrors := 0

	for sc.Scan() {

		line := sc.Text()
		recordsRead++
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

		err = ValidateGameDescriptor(parentCtx, game)
		if err != nil {
			fmt.Println(err)
			validationErrors++
			continue
		}

		games = append(games, game)

		recordsAppended++
	}

	fmt.Println("Records Read", recordsRead, "Records Appended", recordsAppended, "Validation Errors", validationErrors)
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
