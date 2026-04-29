package main

import (
	"fmt"
	"log"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/reports"
)

var Version string = "ref-ledger-v2.1.0"

func main() {

	fmt.Println("Welcome to Ref Ledger V2")

	fmt.Println("Establing database connection...")
	database.InitDbase("refLedger_v2", "mongodb://localhost:27017")

	err := database.Connect()
	if err != nil {
		log.Fatal("db connect: %w", err)
	}

	fmt.Println("Getting context")
	ctx, cancel := database.GetContext()

	defer cancel()

	/*
		game := model.GameDescriptor{
			GameId:      1777,
			Date:        "4/23/2026",
			Time:        "7:45 PM",
			Sport:       "Softball",
			Site:        "Bogan",
			Field:       "7",
			NumOfGames:  1,
			Level:       "Major",
			GameFee:     4750,
			TravelPay:   0,
			AssignorFee: 0,
			Deductions:  0,
			Association: "MSO",
			Status:      "Pending",
			Referee:     "Randall West",
			U1:          "Scott Hentry",
			U2:          "Unassigned",
			ECO:         "Unassinged",
			Assignor:    "Euvonda Harrison",
		}
	*/

	//api.DelGame(ctx, game)
	//api.DelGameTable(ctx)
	//api.AddGame(ctx, game)
	//api.BulkAddGames(ctx, "masterGames.csv")
	//api.DumpGames(ctx)

	//database.SetGameFilters("status", "Completed")
	//database.SetGameFilters("status", "Paid")
	//database.SetGameFilters("u1", "Scott Henry")

	var gameRecords []model.GameDescriptor
	gameRecords, err = database.QueryGames(ctx, "refLedger_v2", "games", "gameFilter.json")

	if err == nil {
		rept := reports.GenerateGameReport(gameRecords)
		reports.PrintReport(rept)
	}

}
