package main

import (
	"errors"
	"net/http"
	"net/mail"
	"ref-ledger-v2/internal/database"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// MobileCatalogRecords uses the web catalog identities and optimistic versions.
func MobileCatalogRecords(w http.ResponseWriter, r *http.Request) {
	tenant, err := getTenantId(r)
	if err != nil || tenant == "" || tenant == "na" {
		http.Error(w, "Account access denied", http.StatusForbidden)
		return
	}
	kind := r.PathValue("type")
	collections := map[string]string{"official": "officials", "association": "associations", "site": "sites"}
	collection, ok := collections[kind]
	if !ok {
		http.Error(w, "Unknown catalog type", http.StatusNotFound)
		return
	}
	coll := database.Client.Database(database.Database).Collection(collection)
	if r.Method == http.MethodGet {
		cursor, err := coll.Find(r.Context(), bson.M{"tenantId": tenant})
		if err != nil {
			http.Error(w, "Unable to load records. Please retry.", http.StatusInternalServerError)
			return
		}
		defer cursor.Close(r.Context())
		records := []interface{}{}
		for cursor.Next(r.Context()) {
			var record interface{}
			switch kind {
			case "official":
				record = &database.OfficialDoc{}
			case "association":
				record = &database.AssociationDoc{}
			case "site":
				record = &database.SiteDoc{}
			}
			if err := cursor.Decode(record); err != nil {
				http.Error(w, "Unable to read records. Please retry.", http.StatusInternalServerError)
				return
			}
			switch doc := record.(type) {
			case *database.OfficialDoc:
				id := doc.OfficialId
				if id == 0 {
					id = doc.Id
				}
				records = append(records, map[string]interface{}{"id": id, "version": doc.Version, "firstName": doc.FirstName, "lastName": doc.LastName, "phone": doc.Phone, "email": doc.Email, "address": doc.Address})
			case *database.AssociationDoc:
				records = append(records, map[string]interface{}{"id": doc.Id, "version": doc.Version, "name": doc.Name, "contact": doc.Contact, "phone": doc.Phone, "email": doc.Email, "assignors": doc.Assignors})
			case *database.SiteDoc:
				records = append(records, map[string]interface{}{"version": doc.Version, "name": doc.Name, "contact": doc.Contact, "phone": doc.Phone, "email": doc.Email})
			}
		}
		if err := cursor.Err(); err != nil {
			http.Error(w, "Unable to read records. Please retry.", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, records)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Record ID is required", http.StatusBadRequest)
		return
	}
	filter := bson.M{"tenantId": tenant}
	switch kind {
	case "official":
		n, parseErr := strconv.ParseInt(id, 10, 64)
		if parseErr != nil || n <= 0 {
			http.Error(w, "Invalid official ID", http.StatusBadRequest)
			return
		}
		filter["$or"] = bson.A{bson.M{"id": n}, bson.M{"officialId": n}}
	case "association":
		filter["id"] = id
	case "site":
		filter["name"] = id
	}
	raw, err := coll.FindOne(r.Context(), filter).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		http.Error(w, "Record not found in this account", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Unable to load record. Please retry.", http.StatusInternalServerError)
		return
	}
	var input struct {
		Version   string `json:"version"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Contact   string `json:"contact"`
		Phone     string `json:"phone"`
		Email     string `json:"email"`
		Address   string `json:"address"`
		Assignors string `json:"assignors"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	if input.Version == "" || database.RecordVersion(raw) != input.Version {
		writeConflict(w, database.ErrConflict)
		return
	}
	if r.Method == http.MethodDelete {
		switch kind {
		case "official":
			var doc database.OfficialDoc
			if err = bson.Unmarshal(raw, &doc); err == nil {
				err = oc.DeleteWithContext(r.Context(), doc.FirstName, doc.LastName, tenant, input.Version)
			}
		case "association":
			err = ac.DeleteWithContext(r.Context(), id, tenant, input.Version)
		case "site":
			err = sc.DeleteWithContext(r.Context(), id, tenant, input.Version)
		}
		if err != nil {
			if !writeConflict(w, err) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	email := strings.TrimSpace(input.Email)
	if email != "" {
		address, emailErr := mail.ParseAddress(email)
		if emailErr != nil || address.Address != email {
			http.Error(w, "Enter a valid email address", http.StatusBadRequest)
			return
		}
	}
	update := bson.M{"phone": strings.TrimSpace(input.Phone), "email": email}
	switch kind {
	case "official":
		var doc database.OfficialDoc
		if err := bson.Unmarshal(raw, &doc); err != nil {
			http.Error(w, "Unable to read record", http.StatusInternalServerError)
			return
		}
		if strings.TrimSpace(input.FirstName) != doc.FirstName || strings.TrimSpace(input.LastName) != doc.LastName {
			http.Error(w, "Official names cannot be changed", http.StatusBadRequest)
			return
		}
		update["address"] = strings.TrimSpace(input.Address)
	case "association":
		if input.ID != id {
			http.Error(w, "Association ID cannot be changed", http.StatusBadRequest)
			return
		}
		update["name"] = strings.TrimSpace(input.Name)
		update["contact"] = strings.TrimSpace(input.Contact)
		update["assignors"] = strings.TrimSpace(input.Assignors)
	case "site":
		if input.Name != id {
			http.Error(w, "Site name cannot be changed", http.StatusBadRequest)
			return
		}
		update["contact"] = strings.TrimSpace(input.Contact)
	}
	trace := database.NewWriteTrace(r.Context(), collection+".update", collection, tenant, "", "", nil)
	result, err := coll.UpdateOne(r.Context(), database.SnapshotFilter(raw), bson.M{"$set": update})
	if err != nil {
		trace.Failure(err, nil, "failed")
		http.Error(w, "Unable to save record. Please retry.", http.StatusInternalServerError)
		return
	}
	if result.MatchedCount == 0 {
		trace.Failure(database.ErrConflict, nil, "unmatched")
		writeConflict(w, database.ErrConflict)
		return
	}
	trace.Success(nil, nil)
	w.WriteHeader(http.StatusNoContent)
}
