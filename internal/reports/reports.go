package reports

import (
	"fmt"
	"ref-ledger-v2/internal/database"
)

type GameTotals struct {
	NumOfGames int64
	GameFees   int64
}

type AssocGameTotalsMap struct {
	AssocGameTotals map[string]map[string]*GameTotals
}

func (g *AssocGameTotalsMap) Init() {

	g.AssocGameTotals = make(map[string]map[string]*GameTotals)

	firstKeys := database.Associations
	secondKeys := database.PermittedGameStatusValues

	for _, fk := range firstKeys {
		g.AssocGameTotals[fk] = make(map[string]*GameTotals)
		for _, sk := range secondKeys {
			g.AssocGameTotals[fk][sk] = &GameTotals{}
		}
	}

}

func (g *AssocGameTotalsMap) Update(assoc, status string, numOfGames, gameFee int64) error {

	if g.AssocGameTotals[assoc] == nil {
		return fmt.Errorf("Association %s not found in table", assoc)
	}

	if g.AssocGameTotals[assoc][status] == nil {
		return fmt.Errorf("Status %s not found in table", status)
	}

	g.AssocGameTotals[assoc][status].NumOfGames += numOfGames
	g.AssocGameTotals[assoc][status].GameFees += gameFee

	return nil
}
