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

	//var bDate string
	//var eDate string

	//
	// Game Flags
	//
	var gfilter = flag.String("gf", "", "JSON filter file used to filter games report")
	var gameIds = flag.String("gi", "", "Game Ids to be used with other flags")
	var gstatus = flag.String("gs", "", "Status game should be set to or used to filter games report")
	var gupdate = flag.String("gu", "", "Update game.")
	var gadd = flag.String("ga", "", "Games Update File.")
	var bdate = flag.String("bd", "", "Beginning date used to filter game report [today | tomorrow | yesterday | this week | next week | last week]")
	var edate = flag.String("ed", "", "Ending date usedd to flter game report")
	var gsport = flag.String("gsport", "", "Sport [Softball | Basketball]")
	var glevel = flag.String("gl", "", "Game Level [SP | JV | Varsity | 9th Grade | PW | Minor | Major | Senior]")

	//
	// Official Flags
	//

	var officialName = flag.String("on", "", "Official Name used to filter officials and games report")

	//
	// Other Flags
	//
	var report = flag.String("rpt", "", "Report to generate [games]")
	var assoc = flag.String("assoc", "", "Association used to filter reports")
	var dumpTable = flag.String("dt", "", "Dump table")
	var sites = flag.String("s", "", "Sites")

	//
	// Flags used to rebuild the various collections
	//
	var rebuild = flag.String("rebuild", "", "Rebuild collection [games]")
	var file = flag.String("file", "", "File used to rebuild collection")

	//
	// Flags for payment
	//
	var paymentId = flag.String("pi", "", "Payment ID")
	var paymentDate = flag.String("pd", "", "Payment Date")
	var paymentAmt = flag.String("pa", "", "Payment Amount")
	var padd = flag.String("padd", "", "Payments Update File")

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
	rootCmd.Flags().StringVar(gfilter, "gf", "", "JSON filter file used to filter games report")
	rootCmd.Flags().StringVar(gameIds, "gi", "", "Game Ids to be used with other flags")
	rootCmd.Flags().StringVar(gstatus, "gs", "", "Status game should be set to or used to filter games report")
	rootCmd.Flags().StringVar(gupdate, "gu", "", "Update game")
	rootCmd.Flags().StringVar(gadd, "ga", "", "Games Update File.")
	rootCmd.Flags().StringVar(bdate, "bd", "", "Beginning date used to filter game report [today | tomorrow | yesterday | this week | next week | last week]")
	rootCmd.Flags().StringVar(edate, "ed", "", "Ending date usedd to flter game report")
	rootCmd.Flags().StringVar(gsport, "gsport", "", "Sport [Softball | Basketball]")
	rootCmd.Flags().StringVar(glevel, "gl", "", "Game Level [SP | JV | Varsity | 9th Grade | PW | Minor | Major | Senior]")

	//
	// Other Flags
	//
	rootCmd.Flags().StringVar(report, "rpt", "", "Report to generate [games]")
	rootCmd.Flags().StringVar(assoc, "assoc", "", "Association used to filter reports")
	rootCmd.Flags().StringVar(dumpTable, "dt", "", "Dump table")
	rootCmd.Flags().StringVar(sites, "s", "", "Sites")

	//
	// Official Flags
	//
	rootCmd.Flags().StringVar(officialName, "on", "", "Official Name used to filter officials and games report")

	//
	// Flags used to rebuild the various collections
	//
	rootCmd.Flags().StringVar(rebuild, "rebuild", "", "Rebuild collection [games]")
	rootCmd.Flags().StringVar(file, "file", "", "File used to rebuild collection")

	//
	// Flags for payment
	//
	rootCmd.Flags().StringVar(paymentId, "pi", "", "Payment ID")
	rootCmd.Flags().StringVar(paymentDate, "pd", "", "Payment Date")
	rootCmd.Flags().StringVar(paymentAmt, "pa", "", "Payment Amount")
	rootCmd.Flags().StringVar(padd, "padd", "", "Payments Update File")

	rootCmd.Execute()

	appLog.Write("Establing database connection...")
	fmt.Println("Establing database connection...")
	database.InitDbase("refLedger_v2", "mongodb://localhost:27017")

	err := database.Connect()
	if err != nil {
		log.Fatal("db connect: %w", err)
	}

	appLog.Write("Getting context")
	fmt.Println("Getting context")
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
	}

	if *gadd != "" {
		api.UpdateGames(ctx, *gadd)
	}

	if *padd != "" {
		api.UpdatePayments(ctx, *padd)
	}

	if *dumpTable != "" {
		api.DumpTable(ctx, *dumpTable)
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
		} else {
			gameFilters.FromDate = bDate
			gameFilters.ToDate = eDate
		}
	}

	if *gstatus != "" {
		gameFilters.Status = *gstatus
	}

	if *assoc != "" {
		gameFilters.Association = *assoc
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

	if *gupdate != "" {
		if len(ids) == 0 {
			fmt.Println("Game Ids not provided.")
			return
		}
		err = api.UpdateGame(ctx, *gupdate, ids)
		if err != nil {
			fmt.Println(err)
		}
	}

	if *paymentId != "" && *paymentAmt != "" && *paymentDate != "" {
		if len(ids) == 0 {
			fmt.Println("Game Ids not provided.")
			return
		}
	}

	switch *report {
	case "games":
		gameRecords, err = database.QueryAggregatedGames(ctx, "refLedger_v2", "games", *gfilter)
		if err != nil {
			return
		}
		rept := reports.GenerateGameReport(gameRecords)
		reports.PrintReport(rept)
		return
	case "payments":
		paymentRecords, err := database.QueryPayments(ctx, "refLedger_v2", "payments")
		if err != nil {
			return
		}
		rept := reports.GeneratePaymentReport(paymentRecords)
		reports.PrintReport(rept)
		return
	case "acctsRecv":
		gFilters := model.GFilters{
			Status: "Completed",
		}

		if *assoc != "" {
			gFilters.Association = *assoc
		}

		*gfilter, err = utils.ConvertGameFiltersToJsonFile(gFilters)
		gameRecords, err = database.QueryAggregatedGames(ctx, "refLedger_v2", "games", *gfilter)
		rept := reports.GenerateAcctsRecvReport(gameRecords)
		reports.PrintReport(rept)
		return
	}

}
