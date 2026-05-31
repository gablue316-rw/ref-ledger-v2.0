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

func DelGame(parentCtx context.Context, game model.GameDescriptor) {

	fmt.Println("Deleting game with Game Id:", game.GameId)
	gameId, _ := utils.ConvertStrToInt64(game.GameId)
	filter := bson.M{"gameId": gameId}

	database.DeleteOneDoc(parentCtx, filter, "refLedger_v2", "games")

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

// Stubbed out for now
func ValidateOfficialDescriptor(parentCtx context.Context, p model.OfficialDescriptor) error {
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
	case "expenses":
		DelExpensesTable(parentCtx)
		BulkAddExpenses(parentCtx, file)
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
		if len(parts) == 3 {
			value = parts[1] + ":" + parts[2]
		}

		filter := bson.M{
			"gameId": bson.M{
				"$in": gameIds,
			},
		}

		if field == "status" && value == "Delete" {
			if len(gameIds) == 1 {
				database.DeleteOneDoc(parentCtx, filter, database.Database, "games")
			} else if len(gameIds) > 1 {
				database.DeleteManyDoc(parentCtx, filter, database.Database, "games")
			}
			return nil
		}

		if field == "status" && value == "Cancelled" {
			database.ClearGames(parentCtx, gameIds)
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
			} else {
				fmt.Println("Successfully updated game with game id[s]:", gameIds)
			}
		}

	}

	return nil

}

func AddPayments(parentCtx context.Context, payment []model.PaymentDescriptor) {

	database.InsertPaymentDocs(parentCtx, payment, database.Database, "payments")
}

func AddExpenses(parentCtx context.Context, expense []model.ExpenseDescriptor) {

	database.InsertExpenseDocs(parentCtx, expense, database.Database, "expenses")
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

func DumpGames(parentCtx context.Context, file string) {

	var games []model.GameDoc = []model.GameDoc{}
	var records []string = []string{}

	var recdFmt string = "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s"

	games, err := database.GetGamesCollection(parentCtx)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Dumping games.  Number of games:", len(games))
	for _, game := range games {

		if file == "" {
			fmt.Println(game)
			continue
		} else {
			g := utils.ConvertGameDocToGameDescr(game)
			r := fmt.Sprintf(recdFmt, g.Date, g.Time, g.GameId, g.Sport, g.Level, g.Site, g.Field, g.NumOfGames, g.GameFee, g.Deductions, g.Status, g.Association, g.Referee, g.U1, g.U2, g.ECO, g.Assignor, g.AssignorFee, g.TravelPay)
			records = append(records, r)
		}
	}
	if len(records) != 0 {
		err = writeToFile(records, file)
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

func DumpTable(parentCtx context.Context, table, file string) {

	switch table {
	case "games":
		DumpGames(parentCtx, file)
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

func generateExpenseId(expense model.ExpenseDescriptor) string {

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

func BulkAddExpenses(parentCtx context.Context, file string) {

	fmt.Println("Peforming Bulk Update for Expenses Collection")
	expenses := []model.ExpenseDescriptor{}
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
		expense := model.ExpenseDescriptor{
			Date:        fields[0],
			Type:        fields[1],
			Amount:      fields[2],
			Association: fields[3],
			GameId:      fields[4],
			Description: fields[5],
		}
		expense.ExpenseId = generateExpenseId(expense)
		expenses = append(expenses, expense)
	}

	AddExpenses(parentCtx, expenses)
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
