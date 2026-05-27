package main

import (
	"flag"
	"fmt"
	"log"

	"ref-ledger-v2/internal/api"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/reports"
	"ref-ledger-v2/internal/utils"

	"ref-ledger-v2/internal/logs"

	"github.com/spf13/cobra"
)

var Version string = "ref-ledger-v2.1.0"

func main() {

	appLog := logs.LogDescriptor{}
	appLog.Open("logs", "ref-ledger-v2.0.log", "append")
	defer appLog.Close()

	var gameRecords []model.GameDescriptor
	var ids []int64
	var gameFilters model.GFilters = model.GFilters{}
	var expenseFilters model.EFilters = model.EFilters{}

	//var bDate string
	//var eDate string

	//
	// Game Flags
	//
	var gfilter = flag.String("j", "", "JSON filter file used to filter games report")
	var gameIds = flag.String("i", "", "Game Ids to be used with other flags")
	var gstatus = flag.String("S", "", "Status used to filter games report")
	var update = flag.String("u", "", "Update single document in collection [games]")
	var value = flag.String("v", "", "Update string [field:value] used to update single document in collection --u")
	var massUpdate = flag.String("m", "", "Mass update of collection using file specifed by --f flag [expense | games | officials | payments]")
	var bdate = flag.String("F", "", "From date used to filter game report [mm/dd/yyyy | today | tomorrow | yesterday | this week | next week | last week]")
	var edate = flag.String("T", "", "To date used to filter game report [mm/dd/yyyy | today | tomorrow | yesterday]")
	var gsport = flag.String("t", "", "Type of Sport [Softball | Basketball]")
	var glevel = flag.String("l", "", "Game Level [SP | JV | Varsity | 9th Grade | PW | Minor | Major | Senior]")

	//
	// Expense Flags
	//

	var expType = flag.String("e", "", "Expense type to filter on [Camp Fee | Dues | Equipment | Food | Mileage]")
	//
	// Official Flags
	//

	var officialName = flag.String("o", "", "Official Name used to filter officials and games report")

	//
	// Other Flags
	//
	var report = flag.String("r", "", "Report to generate [games | payments | acctsRecv | expenses | reconciliation]")
	var assoc = flag.String("a", "", "Association used to filter reports")
	var dumpTable = flag.String("d", "", "Dump table")
	var sites = flag.String("s", "", "Sites")

	//
	// Flags used to rebuild the various collections
	//
	var rebuild = flag.String("R", "", "Rebuild collection [games | expenses | payments | officials]")
	var file = flag.String("f", "", "File used to rebuild collection, dump collection to a file, or write a report to a file")

	//
	// Flags for payment
	//
	var paymentId = flag.String("pi", "", "Payment ID")
	var paymentDate = flag.String("pd", "", "Payment Date")
	var paymentAmt = flag.String("pa", "", "Payment Amount")

	flag.Parse()

	var rootCmd = &cobra.Command{
		Use:   Version,
		Short: "refLedger is an app used by sports officials to keep track of expenses and revenue",
		Long:  "A longer description of refLedger that spans multiple lines and likely contains examples.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Welcome to Ref Ledger", Version)
		},
	}

	//
	// Game Flags
	//
	rootCmd.Flags().StringVar(gfilter, "j", "", "JSON filter file used to filter games report")
	rootCmd.Flags().StringVar(gameIds, "i", "", "Game Ids to be used with other flags")
	rootCmd.Flags().StringVar(gstatus, "S", "", "Status used to filter games report")
	rootCmd.Flags().StringVar(update, "u", "", "Update single document in collection [games]")
	rootCmd.Flags().StringVar(value, "v", "", "Update string [field:value] used to update single document in collection --u")
	rootCmd.Flags().StringVar(massUpdate, "m", "", "Mass update of collection using file specifed by --f flag [expense | games | officials | payments]")
	rootCmd.Flags().StringVar(bdate, "F", "", "From date used to filter game report [mm/dd/yyyy | today | tomorrow | yesterday | this week | next week | last week]")
	rootCmd.Flags().StringVar(edate, "T", "", "To date used to filter game report [mm/dd/yyyy | today | tomorrow | yesterday]")
	rootCmd.Flags().StringVar(gsport, "t", "", "Type of sport [Softball | Basketball]")
	rootCmd.Flags().StringVar(glevel, "l", "", "Game Level [SP | JV | Varsity | 9th Grade | PW | Minor | Major | Senior]")

	//
	// Other Flags
	//
	rootCmd.Flags().StringVar(report, "r", "", "Report to generate [games | payments | acctsRecv | expenses | reconciliation]")
	rootCmd.Flags().StringVar(assoc, "a", "", "Association used to filter reports")
	rootCmd.Flags().StringVar(dumpTable, "d", "", "Dump table")
	rootCmd.Flags().StringVar(sites, "s", "", "Sites")

	//
	// Expense Flags
	//

	rootCmd.Flags().StringVar(expType, "e", "", "Expense type to filter on [Camp Fee | Dues | Equipment | Food | Mileage]")

	//
	// Official Flags
	//
	rootCmd.Flags().StringVar(officialName, "o", "", "Official Name used to filter officials and games report")

	//
	// Flags used to rebuild the various collections
	//
	rootCmd.Flags().StringVar(rebuild, "R", "", "Rebuild collection [games | expenses | payments | officials]")
	rootCmd.Flags().StringVar(file, "f", "", "File used to rebuild collection, dump collection to a file, or write a report to a file")

	//
	// Flags for payment
	//
	rootCmd.Flags().StringVar(paymentId, "pi", "", "Payment ID")
	rootCmd.Flags().StringVar(paymentDate, "pd", "", "Payment Date")
	rootCmd.Flags().StringVar(paymentAmt, "pa", "", "Payment Amount")

	rootCmd.Execute()

	appLog.Write("Establing database connection...", true)
	database.InitDbase("refLedger_v2", "mongodb://localhost:27017")

	err := database.Connect()
	if err != nil {
		log.Fatal("db connect: %w", err)
	}

	appLog.Write("Getting context", true)
	ctx, cancel := database.GetContext()

	defer cancel()

	//
	// Based on flags perform functions
	//
	if *rebuild != "" {

		if *file == "" {
			fmt.Println("Unable to rebuild", *rebuild, "collection.  No file provided for rebuild.")
			return
		}
		api.RebuildTable(ctx, *rebuild, *file)
		return
	}

	if *massUpdate != "" {
		switch *massUpdate {
		case "games":
			api.UpdateGames(ctx, *file)
		case "payments":
			api.UpdatePayments(ctx, *file)
		case "officials":
			api.UpdateOfficials(ctx, *file)
		default:
			fmt.Println("Invalid collection.  Permitted values [games | officials | payments]")
		}
		return
	}

	if *dumpTable != "" {
		api.DumpTable(ctx, *dumpTable, *file)
		return
	}

	//
	// Create Game Filter
	//
	var bDate string = ""
	var eDate string = ""

	if *bdate != "" || *edate != "" {
		bDate, eDate, err = utils.FormatDateFilter(*bdate, *edate)
		if err != nil {
			fmt.Println(err)
			return
		} else {
			gameFilters.FromDate = bDate
			gameFilters.ToDate = eDate
			expenseFilters.FromDate = bDate
			expenseFilters.ToDate = eDate
		}
	}

	if *expType != "" {
		expenseFilters.ExpenseType = *expType
	}

	if *gstatus != "" {
		gameFilters.Status = *gstatus
	}

	if *assoc != "" {
		gameFilters.Association = *assoc
		expenseFilters.Association = *assoc
	}

	if *officialName != "" {
		gameFilters.Official = *officialName
	}

	if *gsport != "" {
		gameFilters.Sport = *gsport
	}

	if *glevel != "" {
		gameFilters.Level = *glevel
	}

	if *gameIds != "" {
		ids, err = utils.ConvertGameIdStrToInt(*gameIds)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		s, _ := utils.ConvertGameIdIntToStr(ids)
		gameFilters.GameId = s
		expenseFilters.GameId = s
	}

	if *sites != "" {
		gameFilters.Site = *sites
	}

	if *gfilter == "" {

		*gfilter, err = utils.ConvertGameFiltersToJsonFile(gameFilters)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	if *update != "" {
		switch *update {
		case "games":
			if len(ids) == 0 {
				fmt.Println("Game Ids not provided.")
				return
			}
			err = api.UpdateGame(ctx, *value, ids)
			if err != nil {
				fmt.Println(err)
			}
			return
		default:
			fmt.Println("Invalid collection.  Permitted values [games]")
		}
	}

	if *paymentId != "" && *paymentAmt != "" && *paymentDate != "" {
		if len(ids) == 0 {
			fmt.Println("Game Ids not provided.")
			return
		}
	}

	var rept []string = []string{}

	switch *report {
	case "games":
		gameRecords, err = database.QueryAggregatedGames(ctx, "refLedger_v2", "games", *gfilter)
		if err != nil {
			return
		}
		rept = reports.GenerateGameReport(gameRecords)
	case "payments":
		paymentRecords, err := database.QueryPayments(ctx, "refLedger_v2", "payments")
		if err != nil {
			return
		}
		rept = reports.GeneratePaymentReport(paymentRecords)
	case "acctsRecv":
		rept = reports.GenerateAcctsRecvReport(ctx, *assoc)
	case "reconciliation":
		paymentRecords, err := database.QueryPayments(ctx, "refLedger_v2", "payments")
		if err != nil {
			return
		}
		rept = reports.GenerateReconciliationReport(paymentRecords)
	case "expenses":
		efilter, err := utils.ConvertExpenseFilterToJsonFile(expenseFilters)
		if err != nil {
			fmt.Println(err)
			return
		}
		expenseRecords, err := database.QueryExpenses(ctx, "refLedger_v2", "expenses", efilter)
		if err != nil {
			fmt.Println(err)
			return
		}
		rept = reports.GenerateExpenseReport(expenseRecords)
	}

	if *file != "" {
		reports.WriteReportToFile(rept, *file)
	} else {
		reports.PrintReport(rept)
	}
}
