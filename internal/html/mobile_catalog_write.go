package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"ref-ledger-v2/internal/database"
	"strings"

	"go.mongodb.org/mongo-driver/mongo"
)

func MobileCatalogCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant, err := getTenantId(r)
	if err != nil || tenant == "" || tenant == "na" {
		http.Error(w, "Account access denied", http.StatusForbidden)
		return
	}

	switch r.PathValue("type") {
	case "official":
		var input database.OfficialJson
		if !mobileDecode(w, r, &input) {
			return
		}
		official := oc.ConvJsonToOfficial(input)
		if err = oc.Add(official, tenant); err != nil {
			mobileCatalogWriteError(w, err)
			return
		}
		records, err := oc.GetOfficialsDirectory(official.FirstName, official.LastName, tenant)
		if err != nil {
			http.Error(w, "Official was saved but could not be reloaded", http.StatusInternalServerError)
			return
		}
		for _, record := range records {
			if strings.EqualFold(record.FirstName, official.FirstName) && strings.EqualFold(record.LastName, official.LastName) {
				writeJSON(w, http.StatusCreated, record)
				return
			}
		}
		http.Error(w, "Official was saved but could not be found", http.StatusInternalServerError)
	case "site":
		var input database.SiteJson
		if !mobileDecode(w, r, &input) {
			return
		}
		site := sc.ConvJsonToSite(input)
		if err = sc.Add(site, tenant); err != nil {
			mobileCatalogWriteError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, site)
	case "association":
		var input database.AssociationJson
		if !mobileDecode(w, r, &input) {
			return
		}
		association := ac.ConvAssocJsonToAssoc(input)
		if err = ac.Add(association, tenant); err != nil {
			mobileCatalogWriteError(w, err)
			return
		}
		created, err := ac.Get(association.Id, tenant)
		if err != nil {
			http.Error(w, "Association was saved but could not be reloaded", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	case "assignor":
		var input CreateAssignorRequest
		if !mobileDecode(w, r, &input) {
			return
		}
		if err = ac.AddAssignor(input.Name, tenant, input.Association); err != nil {
			mobileCatalogWriteError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{
			"association": strings.TrimSpace(input.Association),
			"name":        strings.TrimSpace(input.Name),
		})
	case "level":
		var input database.Level
		if !mobileDecode(w, r, &input) {
			return
		}
		supported, supportErr := uc.IsSportSupported(tenant, strings.TrimSpace(input.Sport))
		if supportErr != nil {
			http.Error(w, "Unable to validate the selected sport", http.StatusInternalServerError)
			return
		}
		if !supported {
			http.Error(w, "Activate this level's sport for the account before adding a level", http.StatusBadRequest)
			return
		}
		if err = lc.Add(input, tenant); err != nil {
			if isMobileCatalogDuplicate(err) {
				mobileLevelDuplicate(w, tenant, input)
				return
			}
			mobileCatalogWriteError(w, err)
			return
		}
		levels, err := lc.GetLevels(tenant, false, input.Sport)
		if err != nil {
			http.Error(w, "Level was saved but could not be reloaded", http.StatusInternalServerError)
			return
		}
		for _, level := range levels {
			if level.Name == strings.TrimSpace(input.Name) {
				writeJSON(w, http.StatusCreated, level)
				return
			}
		}
		http.Error(w, "Level was saved but could not be found", http.StatusInternalServerError)
	case "sport":
		var input struct {
			Sport string `json:"sport"`
		}
		if !mobileDecode(w, r, &input) {
			return
		}
		if err = uc.EnableSportSupported(tenant, input.Sport); err != nil {
			mobileCatalogWriteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"sport": strings.TrimSpace(input.Sport)})
	default:
		http.Error(w, "Unknown catalog type", http.StatusNotFound)
	}
}

func MobileCatalogActivateLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenant, err := getTenantId(r)
	if err != nil || tenant == "" || tenant == "na" {
		http.Error(w, "Account access denied", http.StatusForbidden)
		return
	}
	var input database.Level
	if !mobileDecode(w, r, &input) {
		return
	}
	levels, err := lc.GetLevels(tenant, false)
	if err != nil {
		http.Error(w, "Unable to validate the level", http.StatusInternalServerError)
		return
	}
	var owned *database.Level
	for i := range levels {
		if levels[i].ID == strings.TrimSpace(input.ID) {
			owned = &levels[i]
			break
		}
	}
	if owned == nil {
		http.Error(w, "Level not found in this account", http.StatusNotFound)
		return
	}
	if owned.Active {
		writeJSON(w, http.StatusOK, owned)
		return
	}
	supported, err := uc.IsSportSupported(tenant, owned.Sport)
	if err != nil {
		http.Error(w, "Unable to validate the selected sport", http.StatusInternalServerError)
		return
	}
	if !supported {
		http.Error(w, "Activate this level's sport for the account before activating the level", http.StatusBadRequest)
		return
	}
	activeSports, err := spc.GetSports(true)
	if err != nil {
		http.Error(w, "Unable to validate the active sport catalog", http.StatusInternalServerError)
		return
	}
	active := false
	for _, sport := range activeSports {
		if sport.Name == owned.Sport {
			active = true
			break
		}
	}
	if !active {
		http.Error(w, "Activate the level's sport in the shared catalog before activating the level", http.StatusBadRequest)
		return
	}
	owned.Version = input.Version
	if err = lc.Activate(*owned, tenant); err != nil {
		if errors.Is(err, database.ErrConflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		mobileCatalogWriteError(w, err)
		return
	}
	levels, err = lc.GetLevels(tenant, false, owned.Sport)
	if err != nil {
		http.Error(w, "Level was activated but could not be reloaded", http.StatusInternalServerError)
		return
	}
	for _, level := range levels {
		if level.ID == owned.ID {
			writeJSON(w, http.StatusOK, level)
			return
		}
	}
	http.Error(w, "Level was activated but could not be found", http.StatusInternalServerError)
}

func mobileLevelDuplicate(w http.ResponseWriter, tenant string, input database.Level) {
	levels, err := lc.GetLevels(tenant, false, input.Sport)
	if err != nil {
		http.Error(w, "A matching level exists, but its activation status could not be loaded", http.StatusInternalServerError)
		return
	}
	for _, level := range levels {
		if strings.EqualFold(level.Name, strings.TrimSpace(input.Name)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "A level with this name already exists for the selected sport.",
				"level":   level,
			})
			return
		}
	}
	http.Error(w, "A matching level already exists for the selected sport.", http.StatusConflict)
}

func isMobileCatalogDuplicate(err error) bool {
	if mongo.IsDuplicateKeyError(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func mobileCatalogWriteError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if isMobileCatalogDuplicate(err) {
		status = http.StatusConflict
	}
	http.Error(w, err.Error(), status)
}
