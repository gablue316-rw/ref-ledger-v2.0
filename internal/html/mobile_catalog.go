package main

import (
	"net/http"
	"ref-ledger-v2/internal/database"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

type mobileAssociationOption struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Assignors []string `json:"assignors"`
}

// MobileCatalog reads the same account collections as the web game form.
// Only dropdown fields are returned; account contact and login data stay private.
func MobileCatalog(w http.ResponseWriter, r *http.Request) {
	tenant, err := getTenantId(r)
	if err != nil || tenant == "" {
		http.Error(w, "Account access denied", 403)
		return
	}
	db := database.Client.Database(database.Database)
	var user struct {
		Sports []string `bson:"sports"`
	}
	if err = db.Collection("users").FindOne(r.Context(), bson.M{"tenantId": tenant}).Decode(&user); err != nil {
		http.Error(w, "Unable to load selected sports", 500)
		return
	}
	associations := []database.AssociationDoc{}
	sites := []database.SiteDoc{}
	levels := []database.Level{}
	read := func(collection string, filter bson.M, records interface{}) error {
		cursor, err := db.Collection(collection).Find(r.Context(), filter)
		if err != nil {
			return err
		}
		defer cursor.Close(r.Context())
		return cursor.All(r.Context(), records)
	}
	if err = read("associations", bson.M{"tenantId": tenant}, &associations); err != nil {
		http.Error(w, "Unable to load associations", 500)
		return
	}
	if err = read("sites", bson.M{"tenantId": tenant}, &sites); err != nil {
		http.Error(w, "Unable to load sites", 500)
		return
	}
	if err = read("levels", bson.M{"tenantId": tenant, "active": true}, &levels); err != nil {
		http.Error(w, "Unable to load levels", 500)
		return
	}
	choices := []mobileAssociationOption{}
	for _, association := range associations {
		choices = append(choices, mobileAssociationOption{ID: association.Id, Name: association.Name, Assignors: mobileUniqueNames(strings.Split(association.Assignors, ","))})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].ID < choices[j].ID })
	names := []string{}
	for _, site := range sites {
		names = append(names, site.Name)
	}
	sort.Slice(levels, func(i, j int) bool {
		if levels[i].Sport == levels[j].Sport {
			return levels[i].Name < levels[j].Name
		}
		return levels[i].Sport < levels[j].Sport
	})
	writeJSON(w, 200, map[string]interface{}{"sports": mobileUniqueNames(user.Sports), "associations": choices, "sites": mobileUniqueNames(names), "levels": levels})
}
func mobileUniqueNames(names []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}
