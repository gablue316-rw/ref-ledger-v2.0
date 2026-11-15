package api

import (
	"context"
	"fmt"
	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

var ApiVersion string = "ref-ledger-api-v2.1.0"
var oc database.OfficialCollection
var ac database.AssociationCollection
var gc database.GameCollection

var OcInitialized bool = false
var AcInitialized bool = false
var GcInitialized bool = false

func DelGame(parentCtx context.Context, gameId, tId string) {
	if tId == "" || tId == "na" {
		fmt.Println("DelGame: Invalid tenant ID")
		return
	}

	gId, err := utils.ConvertStrToInt64(gameId)
	if err != nil || gId <= 0 {
		fmt.Printf("DelGame: Invalid game ID %q\n", gameId)
		return
	}

	fmt.Println("Deleting game with Game Id:", gId, "for tenant:", tId)

	filter := bson.M{
		"gameId":   gId,
		"tenantId": tId,
	}

	database.DeleteOneDoc(
		parentCtx,
		filter,
		database.GetMongoDbName(),
		"games",
	)
}

func ValidateGameDescriptor(
	parentCtx context.Context,
	g model.GameDescriptor,
	checkForDup bool,
	tId string,
) error {
	const errorFormat = "%s %s not found! Record Dropped!"

	dollarAmountPattern := regexp.MustCompile(
		`^\$?[0-9]+(\.[0-9]{1,2})?$`,
	)

	// Require a valid game date within the permitted year range.
	gameDateText := strings.TrimSpace(g.Date)
	if gameDateText == "" {
		return fmt.Errorf("game date is required")
	}

	gameDate, err := time.Parse("1/2/2006", gameDateText)
	if err != nil {
		return fmt.Errorf(
			"invalid game date %q: expected M/D/YYYY",
			gameDateText,
		)
	}

	if gameDate.Year() < utils.MinTaxYear ||
		gameDate.Year() > utils.MaxTaxYear {
		return fmt.Errorf(
			"invalid game date; expected a date between 1/1/%d and 12/31/%d",
			utils.MinTaxYear,
			utils.MaxTaxYear,
		)
	}

	// Default a blank tax year to the game date's year.
	taxYearText := strings.TrimSpace(g.TaxYear)
	if taxYearText == "" {
		taxYearText = strconv.Itoa(gameDate.Year())
	}

	taxYear, err := strconv.Atoi(taxYearText)
	if err != nil ||
		len(taxYearText) != 4 ||
		taxYear < utils.MinTaxYear ||
		taxYear > utils.MaxTaxYear {
		return utils.ErrInvalidTaxYear
	}

	if !GcInitialized {
		if err := gc.Init(database.Client); err != nil {
			return fmt.Errorf(
				"games collection is not initialized: %w",
				err,
			)
		}
		GcInitialized = true
	}

	if !OcInitialized {
		if err := oc.Init(database.Client); err != nil {
			return fmt.Errorf(
				"officials collection is not initialized: %w",
				err,
			)
		}
		OcInitialized = true
	}

	if !AcInitialized {
		if err := ac.Init(database.Client); err != nil {
			return fmt.Errorf(
				"association collection is not initialized: %w",
				err,
			)
		}
		AcInitialized = true
	}

	if !dollarAmountPattern.MatchString(g.GameFee) {
		return fmt.Errorf(
			"invalid Game Fee amount %q: use numbers with an optional $ and up to two decimal places",
			g.GameFee,
		)
	}

	if !dollarAmountPattern.MatchString(g.TravelPay) {
		return fmt.Errorf(
			"invalid Travel Pay amount %q: use numbers with an optional $ and up to two decimal places",
			g.TravelPay,
		)
	}

	if !dollarAmountPattern.MatchString(g.AssignorFee) {
		return fmt.Errorf(
			"invalid Assignor Fee amount %q: use numbers with an optional $ and up to two decimal places",
			g.AssignorFee,
		)
	}

	if !dollarAmountPattern.MatchString(g.Deductions) {
		return fmt.Errorf(
			"invalid Deductions amount %q: use numbers with an optional $ and up to two decimal places",
			g.Deductions,
		)
	}

	assignor := strings.TrimSpace(g.Assignor)
	gameIDText := strings.TrimSpace(g.GameId)
	association := strings.TrimSpace(g.Association)

	gameID, err := strconv.ParseInt(gameIDText, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid game ID: %q", gameIDText)
	}

	fmt.Println(
		"Validating game with game ID",
		gameIDText,
		"for association",
		association,
	)

	// For new games, check for the same ID, tenant, and association.
	if checkForDup {
		exists, err := gc.Exists(association, gameIDText, tId)
		if err != nil {
			return fmt.Errorf(
				"failed to check for duplicate game: %w",
				err,
			)
		}

		if exists {
			return fmt.Errorf(
				"duplicate game: game ID %s for association %s",
				gameIDText,
				association,
			)
		}
	}

	// Check the requested date and time for both adds and updates.
	matches, err := gc.GetGameIdsByDateTime(tId, g.Date, g.Time)
	if err != nil {
		return fmt.Errorf(
			"error retrieving games by date and time: %w",
			err,
		)
	}

	for _, existingGame := range matches {
		isCurrentGame :=
			existingGame.GameId == gameID &&
				existingGame.Association == association

		// An update may match itself, but not another game.
		if !checkForDup && isCurrentGame {
			continue
		}

		return fmt.Errorf(
			"duplicate date and time: game %d for association %s already exists on %s at %s",
			existingGame.GameId,
			existingGame.Association,
			g.Date,
			g.Time,
		)
	}

	for _, assignment := range g.Officials {
		role := strings.TrimSpace(assignment.Title)
		if role == "" {
			role = assignment.RoleId
		}

		officialID := strings.TrimSpace(assignment.OfficialId)
		name := strings.TrimSpace(assignment.Name)

		if officialID == "Unassigned" {
			officialID = ""
		}

		if officialID == "" &&
			(name == "" || name == "Unassigned") {
			continue
		}

		if officialID != "" {
			id, err := strconv.ParseInt(officialID, 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf(
					"invalid officialId %q for role %q",
					officialID,
					role,
				)
			}

			if _, err := oc.GetById(id, tId); err != nil {
				return fmt.Errorf(
					"invalid official for role %q: %w",
					role,
					err,
				)
			}

			continue
		}

		// Support imports containing a name without an official ID.
		parts := strings.Fields(name)
		if len(parts) < 2 {
			return fmt.Errorf(
				"invalid official name %q for role %q",
				name,
				role,
			)
		}

		if _, err := oc.Get(
			parts[0],
			parts[len(parts)-1],
			tId,
		); err != nil {
			return fmt.Errorf(
				"invalid official %q for role %q: %w",
				name,
				role,
				err,
			)
		}
	}

	if assignor != "Unassigned" {
		exists, err := ac.AssignorExists(
			assignor,
			tId,
			association,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to validate assignor %q: %w",
				assignor,
				err,
			)
		}

		if !exists {
			return fmt.Errorf(errorFormat, "Assignor", assignor)
		}
	}

	return nil
}
