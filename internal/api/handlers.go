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

	"encoding/csv"

	"go.mongodb.org/mongo-driver/bson"
)

var ApiVersion string = "ref-ledger-api-v2.1.0"
var oc database.OfficialCollection
var ac database.AssociationCollection
var gc database.GameCollection

var OcInitialized bool = false
var AcInitialized bool = false
var GcInitialized bool = false

func GetAssociations(parentCtx context.Context) (string, error) {
	fmt.Println("Getting Associations")

	associations := []string{"GOLLC", "MCBOA", "MSO"}

	return strings.Join(associations, ","), nil

}

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

func DelGame(parentCtx context.Context, gameId string) {

	fmt.Println("Deleting game with Game Id:", gameId)
	gId, _ := utils.ConvertStrToInt64(gameId)
	filter := bson.M{"gameId": gId}

	database.DeleteOneDoc(parentCtx, filter, database.GetMongoDbName(), "games")

}

func UpdateOfficials(parentCtx context.Context, file string) error {

	fmt.Println("Adding to Officials Collection")

	officials := []model.OfficialDescriptor{}

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

		official := model.OfficialDescriptor{
			FirstName:   fields[0],
			LastName:    fields[1],
			Phone:       fields[2],
			Association: fields[3],
		}

		err = ValidateOfficialDescriptor(parentCtx, official)
		if err != nil {
			fmt.Println(err)
			validationErrors++
			continue
		}

		officials = append(officials, official)

		recordsAppended++
	}

	fmt.Println("Records Read", recordsRead, "Records Deleted", recordsDeleted, "Records Appended", recordsAppended, "Validation Errors", validationErrors)
	AddOfficials(parentCtx, officials)

	return nil

}

func UpdateGamesFromJsonFile(parentCtx context.Context, file string) error {

	fmt.Println("Adding to Games Collection using Json File")

	games, err := utils.ConvertJsonToGameDescriptor(file)
	newGames := []model.GameDescriptor{}

	if err != nil {
		return fmt.Errorf("Failed to convert json file %s to GameDescriptor.  Reason: %s", file, err)
	}

	recordsDeleted := 0
	validationErrors := 0
	recordsAppended := 0
	recordsRead := 0

	for _, g := range games {

		recordsRead++

		if g.Status == "Delete" {
			DelGame(parentCtx, g.GameId)
			recordsDeleted++
			continue
		}

		checkForDup := true
		err = ValidateGameDescriptor(parentCtx, g, checkForDup)
		if err != nil {
			fmt.Println(err)
			validationErrors++
			continue
		}

		newGames = append(newGames, g)

		recordsAppended++
	}

	fmt.Println("Records Read", recordsRead, "Records Deleted", recordsDeleted, "Records Appended", recordsAppended, "Validation Errors", validationErrors)
	if len(newGames) > 0 {
		AddGames(parentCtx, newGames)
	}

	return nil

}

// Stubbed out for now
func ValidateOfficialDescriptor(parentCtx context.Context, p model.OfficialDescriptor) error {
	return nil
}

func ValidateGameDescriptor(parentCtx context.Context, g model.GameDescriptor, checkForDup bool) error {

	var errorFormat string = "%s %s not found!  Record Dropped!"
	var tId string = database.TenantId

	if !GcInitialized {
		err := gc.Init(database.Client)
		if err != nil {
			fmt.Println("Games Collection is not initialized")
			return fmt.Errorf("Games Collection is not initialized")
		}
		GcInitialized = true
	}

	if !OcInitialized {
		err := oc.Init(database.Client)
		if err != nil {
			fmt.Println("Officials Collection is not initialized")
			return fmt.Errorf("Officials Collection is not initialized")
		}
		OcInitialized = true
	}

	if !AcInitialized {
		err := ac.Init(database.Client)
		if err != nil {
			fmt.Println("Association Collection is not initialized")
			return fmt.Errorf("Association Collection is not initialized")
		}
		AcInitialized = true
	}

	var assignor string = strings.TrimSpace(g.Assignor)
	var gameid string = strings.TrimSpace(g.GameId)
	var assoc string = strings.TrimSpace(g.Association)

	fmt.Println("Validating Game with game id", gameid, "for association", assoc)

	if checkForDup {
		exists, err := gc.Exists(assoc, gameid, tId)
		if err != nil {
			return fmt.Errorf("failed to check for duplicate game: %w", err)
		}
		if exists {
			return fmt.Errorf(
				"duplicate game: game ID %s for association %s",
				gameid, assoc,
			)
		}
	}

	// Retrieve game IDs by date and time to check for duplicates based on date and time
	//
	fmt.Println("Verifying that there are no duplicate games for the same date and time")
	fmt.Println("Getting game IDs by date and time")
	gameIds, err := gc.GetGameIdsByDateTime(tId, g.Date, g.Time)
	fmt.Println("Game IDs retrieved:", gameIds)

	if err != nil {
		return fmt.Errorf("error retrieving game IDs by date and time: %s", err.Error())
	}

	gameId, err := strconv.ParseInt(g.GameId, 10, 64)

	if err != nil {
		return fmt.Errorf("invalid game ID: %s", g.GameId)
	}

	fmt.Println("Checking for Game Ids with duplicate date and time")
	for _, existingGameId := range gameIds {
		if existingGameId != gameId {
			return fmt.Errorf("duplicate game found for the same date and time")
		}
	}

	for _, assignment := range g.Officials {
		role := strings.TrimSpace(assignment.Title)
		if role == "" {
			role = assignment.RoleId
		}

		officialId := strings.TrimSpace(assignment.OfficialId)
		name := strings.TrimSpace(assignment.Name)

		if officialId == "Unassigned" {
			officialId = ""
		}

		if officialId == "" && (name == "" || name == "Unassigned") {
			continue
		}

		if officialId != "" {
			id, err := strconv.ParseInt(officialId, 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf(
					"invalid officialId %q for role %q",
					officialId, role,
				)
			}

			if _, err := oc.GetById(id, tId); err != nil {
				return fmt.Errorf(
					"invalid official for role %q: %w",
					role, err,
				)
			}

			continue
		}

		// Support imports containing a name within the Officials array.
		parts := strings.Fields(name)
		if len(parts) < 2 {
			return fmt.Errorf(
				"invalid official name %q for role %q",
				name, role,
			)
		}

		if _, err := oc.Get(
			parts[0],
			parts[len(parts)-1],
			tId,
		); err != nil {
			return fmt.Errorf(
				"invalid official %q for role %q: %w",
				name, role, err,
			)
		}
	}
	fmt.Println("Assignor=", assignor)
	if assignor != "Unassigned" {
		results, err := ac.AssignorExists(assignor, tId, g.Association)
		if !results || err != nil {
			return fmt.Errorf(errorFormat, "Assignor", assignor)
		}
	}
	return nil
}

func UpdatePayment(parentCtx context.Context) {

}

func UpdateGame(
	parentCtx context.Context,
	cmd string,
	gameIds []int64,
) error {
	if len(gameIds) == 0 {
		return fmt.Errorf("no game IDs were supplied")
	}

	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("no update command was supplied")
	}

	tId := database.TenantId

	if !GcInitialized {
		if err := gc.Init(database.Client); err != nil {
			return fmt.Errorf("initialize games collection: %w", err)
		}
		GcInitialized = true
	}

	type gameChange struct {
		field string
		value string
	}

	changes := make([]gameChange, 0)

	for _, command := range strings.Split(cmd, ";") {
		if strings.TrimSpace(command) == "" {
			continue
		}

		parts := strings.SplitN(command, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf(
				"invalid command %q: expected field:value",
				command,
			)
		}

		field := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch field {
		case "referee", "u1", "u2", "eco":
			return fmt.Errorf(
				"field %q is deprecated; update the Officials array through the game editor",
				field,
			)

		case "date", "time", "site", "field", "level",
			"home", "visitor", "assignor":
			// Supported string fields.

		case "numOfGames":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid number of games %q", value)
			}
			value = strconv.FormatInt(n, 10)

		case "gameFee", "travelPay", "assignorFee", "deductions":
			amount, err := utils.ConvertAmtStrToInt64(value)
			if err != nil {
				return fmt.Errorf("invalid %s: %w", field, err)
			}
			value = utils.ConvertInt64ToAmtStr(amount)

		case "status":
			switch value {
			case "Pending", "Completed", "Paid", "Cancelled", "Delete":
			default:
				return fmt.Errorf("invalid game status %q", value)
			}

		default:
			return fmt.Errorf("unsupported update field %q", field)
		}

		changes = append(changes, gameChange{
			field: field,
			value: value,
		})
	}

	if len(changes) == 0 {
		return fmt.Errorf("no update command was supplied")
	}

	// Deletion must be a separate command.
	for _, change := range changes {
		if change.field == "status" && change.value == "Delete" &&
			len(changes) != 1 {
			return fmt.Errorf("Delete must be supplied as the only command")
		}
	}

	for _, id := range gameIds {
		if err := parentCtx.Err(); err != nil {
			return err
		}

		// Scope the lookup to the current tenant.
		var doc model.GameDoc
		err := database.Client.
			Database(database.Database).
			Collection("games").
			FindOne(parentCtx, bson.M{
				"tenantId": tId,
				"gameId":   id,
			}).
			Decode(&doc)
		if err != nil {
			return fmt.Errorf("retrieve game %d: %w", id, err)
		}

		if changes[0].field == "status" &&
			changes[0].value == "Delete" {
			_, err := database.Client.
				Database(database.Database).
				Collection("games").
				DeleteOne(parentCtx, bson.M{
					"tenantId":    tId,
					"gameId":      id,
					"association": doc.Association,
				})
			if err != nil {
				return fmt.Errorf("delete game %d: %w", id, err)
			}
			continue
		}

		game := utils.ConvertGameDocToGameDescr(doc)

		for _, change := range changes {
			switch change.field {
			case "date":
				game.Date = change.value
			case "time":
				game.Time = change.value
			case "site":
				game.Site = change.value
			case "field":
				game.Field = change.value
			case "level":
				game.Level = change.value
			case "home":
				game.Home = change.value
			case "visitor":
				game.Visitor = change.value
			case "assignor":
				game.Assignor = change.value
			case "numOfGames":
				game.NumOfGames = change.value
			case "gameFee":
				game.GameFee = change.value
			case "travelPay":
				game.TravelPay = change.value
			case "assignorFee":
				game.AssignorFee = change.value
			case "deductions":
				game.Deductions = change.value
			case "status":
				game.Status = change.value
			}
		}

		if game.Status == "Cancelled" {
			game.GameFee = "0.00"
			game.TravelPay = "0.00"
			game.AssignorFee = "0.00"
			game.Deductions = "0.00"
		}

		if err := ValidateGameDescriptor(parentCtx, game, false); err != nil {
			return fmt.Errorf("validate game %d: %w", id, err)
		}

		if err := database.UpdateOneGameDoc(
			parentCtx,
			game,
			database.Database,
			"games",
			tId,
		); err != nil {
			return fmt.Errorf("update game %d: %w", id, err)
		}
	}

	return nil
}

func AddGames(parentCtx context.Context, game []model.GameDescriptor) {

	database.InsertGameDocs(parentCtx, game, database.Database, "games", database.TenantId)
}

func AddOfficials(parentCtx context.Context, official []model.OfficialDescriptor) {

	database.InsertOfficialDocs(parentCtx, official, database.Database, "officials")
}

func DelGamesTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "games")

}

func DelExpensesTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "expenses")

}

func DelOfficialsTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "officials")

}

func DelPaymentsTable(parentCtx context.Context) {

	database.DelCollection(parentCtx, database.Database, "payments")

}

func writeToFile(records []string, file string) error {

	content := strings.Join(records, "\n")
	err := os.WriteFile(file, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("Failed to write to file %s.  Reason: %v", file, err)
	}

	return nil
}

func DumpGames(
	parentCtx context.Context,
	file, assoc, format string,
) {
	games, err := database.GetGamesCollection(parentCtx, assoc)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Dumping games. Number of games:", len(games))

	if file == "" {
		for _, game := range games {
			fmt.Println(game)
		}
		return
	}

	var output strings.Builder
	writer := csv.NewWriter(&output)

	for _, game := range games {
		g := utils.ConvertGameDocToGameDescr(game)

		officials := g.Officials
		if officials == nil {
			officials = []model.GameOfficialDoc{}
		}

		officialsJSON, err := json.Marshal(officials)
		if err != nil {
			fmt.Printf(
				"Failed to encode officials for game %s: %v\n",
				g.GameId, err,
			)
			return
		}

		err = writer.Write([]string{
			g.Date,
			g.Time,
			g.GameId,
			g.Sport,
			g.Level,
			g.Site,
			g.Field,
			g.NumOfGames,
			g.GameFee,
			g.Deductions,
			g.Status,
			g.Association,
			string(officialsJSON),
			g.Assignor,
			g.AssignorFee,
			g.TravelPay,
		})
		if err != nil {
			fmt.Println("Failed to write game CSV:", err)
			return
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		fmt.Println("Failed to write game CSV:", err)
		return
	}

	if err := os.WriteFile(file, []byte(output.String()), 0644); err != nil {
		fmt.Println("Failed to save game CSV:", err)
	}
}

func DumpOfficials(parentCtx context.Context, file string) {

	var officials []model.OfficialDoc = []model.OfficialDoc{}
	var records []string = []string{}

	var recdFmt string = "%s,%s,%s,%s"

	officials, err := database.GetOfficialsCollection(parentCtx)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Dumping officials.  Number of officials:", len(officials))
	for _, official := range officials {

		if file == "" {
			fmt.Println(official)
			continue
		} else {
			o := utils.ConvertOfficialDocToOfficialDescr(official)
			r := fmt.Sprintf(recdFmt, o.FirstName, o.LastName, o.Phone, o.Association)
			records = append(records, r)
		}
	}

	if len(records) != 0 {
		err = writeToFile(records, file)
		if err != nil {
			fmt.Println(err)
		}
	}

}

func DumpPayments(parentCtx context.Context, file string) {

	var payments []model.PaymentDoc = []model.PaymentDoc{}
	var records []string = []string{}

	var recdFmt string = "%s,%s,%s,%s,%s"

	payments, err := database.GetPaymentsCollection(parentCtx)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Dumping payments.  Number of payments:", len(payments))
	for _, payment := range payments {

		if file == "" {
			fmt.Println(payment)
			continue
		} else {
			p := utils.ConvertPaymentDocToPaymentDescr(payment)
			r := fmt.Sprintf(recdFmt, p.PaymentId, p.PaymentDate, p.PaymentAmt, p.Association, p.GameIds)
			records = append(records, r)
		}
	}

	if len(records) != 0 {
		err = writeToFile(records, file)
		if err != nil {
			fmt.Println(err)
		}
	}

}

func DumpExpenses(parentCtx context.Context, file string) {

	var expenses []model.ExpenseDoc = []model.ExpenseDoc{}
	var records []string = []string{}

	var recdFmt string = "%s,%s,%s,%s,%s,%s"

	expenses, err := database.GetExpensesCollection(parentCtx)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Dumping expenses.  Number of expenses:", len(expenses))
	for _, expense := range expenses {

		if file == "" {
			fmt.Println(expense)
			continue
		} else {
			e := utils.ConvertExpenseDocToExpenseDescr(expense)
			r := fmt.Sprintf(recdFmt, e.Date, e.Type, e.Amount, e.Association, e.GameId, e.Description)
			records = append(records, r)
		}
	}

	if len(records) != 0 {
		err = writeToFile(records, file)
		if err != nil {
			fmt.Println(err)
		}
	}

}

func DumpTable(parentCtx context.Context, table, file, assoc, format string) {

	switch table {
	case "games":
		DumpGames(parentCtx, file, assoc, format)
	case "officials":
		DumpOfficials(parentCtx, file)
	case "payments":
		DumpPayments(parentCtx, file)
	case "expenses":
		DumpExpenses(parentCtx, file)
	default:
		fmt.Println("Table", table, "not supported")
	}

}

func GenerateExpenseId(expense model.ExpenseDescriptor) string {

	var month int
	var day int
	var year int
	var expTypeId string
	var dollar string
	var cents string
	var expenseId string = ""

	expTypeParts := strings.Split(expense.Type, " ")
	if len(expTypeParts) > 1 {
		expTypeId = expTypeParts[0][0:1] + expTypeParts[1][0:1]
	} else if len(expTypeParts) == 1 {
		expTypeId = expTypeParts[0][0:2]
	}

	dollarCents := strings.Split(expense.Amount, ".")

	if len(dollarCents) == 2 {
		dollar = dollarCents[0]
		cents = dollarCents[1]
	} else {
		dollar = expense.Amount
		cents = "00"
	}

	n, err := fmt.Sscanf(expense.Date, "%d/%d/%d", &month, &day, &year)
	if err != nil || n != 3 {
		fmt.Println("Error parsing date for expense", expense.ExpenseId, "Reason:", err)
		return fmt.Sprintf("EXP%s", utils.GenerateRandomString(10))
	}
	if len(expense.Association) != 0 {
		expTypeId = expTypeId + "-" + expense.Association
	}
	expenseId = fmt.Sprintf("%s-%d%d%d-%s%s", expTypeId, month, day, year, dollar, cents)
	return expenseId

}
