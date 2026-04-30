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

	var gfilter = flag.String("gf", "", "JSON filter file used to filter games report")
	var gameIds = flag.String("gid", "", "Game Ids to be used with other flags")
	var report = flag.String("rpt", "", "Report to generate [games]")
	var gstatus = flag.String("gs", "", "Status game should be set to")

	flag.Parse()

	var rootCmd = &cobra.Command{
		Use:   Version,
		Short: "refLedger is an app used by sports officials to keep track of expenses and revenue",
		Long:  "A longer description of refLedger that spans multiple lines and likely contains examples.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Welcome to Ref Ledger", Version)
		},
	}

	rootCmd.Flags().StringVar(gfilter, "gf", "", "JSON filter file used to filter games report")
	rootCmd.Flags().StringVar(gameIds, "gid", "", "Game Ids to be used with other flags")
	rootCmd.Flags().StringVar(report, "rpt", "", "Report to generater [games]")
	rootCmd.Flags().StringVar(gstatus, "gs", "", "Status game should be set to")

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

	var gameRecords []model.GameDescriptor

	if *report == "games" {
		gameRecords, err = database.QueryGames(ctx, "refLedger_v2", "games", *gfilter)
		if err != nil {
			return
		}
		rept := reports.GenerateGameReport(gameRecords)
		reports.PrintReport(rept)
		return
	}

	var ids []int64
	if *gameIds != "" {
		ids, err = utils.ConvertGameIdStrToInt(*gameIds)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
	}

	if *gstatus != "" {
		if len(ids) == 0 {
			fmt.Println("Game Ids not provided.")
			return
		}
		err = api.UpdateGameStatus(ctx, *gstatus, ids)
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
