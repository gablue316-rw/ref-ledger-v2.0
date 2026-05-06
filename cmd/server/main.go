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

	"github.com/spf13/cobra"
)

var Version string = "ref-ledger-v2.1.0"

func main() {

	var gameRecords []model.GameDescriptor
	var ids []int64
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
	var bdate = flag.String("bd", "", "Beginning date used to filter game report [today | tomorrow | yesterday]")
	var edate = flag.String("ed", "", "Ending date usedd to flter game report [today | tomorrow | yesterday]")

	//
	// Official Flags
	//

	var officialName = flag.String("on", "", "Official Name used to filter officials report")

	//
	// Other Flags
	//
	var report = flag.String("rpt", "", "Report to generate [games]")
	var assoc = flag.String("assoc", "", "Association used to filter reports")
	var dumpTable = flag.String("dt", "", "Dump table")

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
	rootCmd.Flags().StringVar(bdate, "bd", "", "Beginning date used to filter game report [today | tomorrow | yesterday]")
	rootCmd.Flags().StringVar(edate, "ed", "", "Ending date usedd to flter game report [today | tomorrow | yesterday]")

	//
	// Other Flags
	//
	rootCmd.Flags().StringVar(report, "rpt", "", "Report to generate [games]")
	rootCmd.Flags().StringVar(assoc, "assoc", "", "Association used to filter reports")
	rootCmd.Flags().StringVar(dumpTable, "dt", "", "Dump table")

	//
	// Official Flags
	//
	rootCmd.Flags().StringVar(officialName, "on", "", "Official Name used to filter officials report")

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

	rootCmd.Execute()

	fmt.Println("Establing database connection...")
	database.InitDbase("refLedger_v2", "mongodb://localhost:27017")

	err := database.Connect()
	if err != nil {
		log.Fatal("db connect: %w", err)
	}

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

	if *dumpTable != "" {
		api.DumpTable(ctx, *dumpTable)
	}

	var bDate string = ""
	var eDate string = ""

	if *bdate != "" || *edate != "" {
		bDate, eDate, err = utils.FormatDateFilter(*bdate, *edate)
	}

	if *gameIds != "" {
		ids, err = utils.ConvertGameIdStrToInt(*gameIds)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
	}

	if *report == "games" {

		//
		// If the user didn't enter a json game filter file, check if the user
		// entered other filter flags
		//
		if *gfilter == "" {
			if *gameIds != "" || *gstatus != "" || *assoc != "" || *bdate != "" || *edate != "" {
				s, _ := utils.ConvertGameIdIntToStr(ids)
				*gfilter, err = utils.ConvertGameFiltersToJsonFile(*assoc, s, *gstatus, bDate, eDate)
				if err != nil {
					fmt.Println(err)
					return
				}
			}
		}

		gameRecords, err = database.QueryAggregatedGames(ctx, "refLedger_v2", "games", *gfilter)
		if err != nil {
			return
		}
		rept := reports.GenerateGameReport(gameRecords)
		reports.PrintReport(rept)
		return
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

	/*
		var gameDocs []model.GameDoc
		for _, v := range gameRecords {

			doc := utils.ConvertGameDescrToGameDoc(v)
			gameDocs = append(gameDocs, doc)
		}

		err = utils.ConvertGameDocToJson(gameDocs, "test.json")
		if err != nil {
			fmt.Println(err)
		}
	*/

}
