// Place in a directory beneath the ref-ledger-v2 module, for example
// tests/metrics/scalability-test-v2/main.go, or build this filename explicitly.
//
// go build -o scalability-test-v2.exe ./tests/metrics/scalability-test-v2
// ./scalability-test-v2.exe -users 10 -database refLedger_test -duration 30m
// ./scalability-test-v2.exe -users 0 -database refLedger_test
//
// DESTRUCTIVE: every run clears ALL documents in the selected database.
// Collections/indexes remain. No database cleanup occurs at shutdown.
// MONGODB_URI is used when set; otherwise database.BuildMongoURI uses the
// application's MONGODB_ENV/HOST/USERNAME/PASSWORD/REPLICA_SET settings.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"ref-ledger-v2/tests/internal/testcatalog"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var collections = []string{"associations", "expenses", "games", "officials", "sites", "payments"}
var sequence int64
var errEmpty = errors.New("no eligible records")

type counts struct{ Adds, Changes, Deletes, Errors int64 }

func (c counts) String() string {
	return fmt.Sprintf("%d/%d/%d/%d", c.Adds, c.Changes, c.Deletes, c.Errors)
}

type actor struct {
	Username, TenantID string
	Stats              [6]counts // Owned by this user's goroutine; read only after Wait.
	rng                *rand.Rand
	ac                 database.AssociationCollection
	sc                 database.SiteCollection
	oc                 database.OfficialCollection
	gc                 database.GameCollection
	ec                 database.ExpensesCollection
}

type harness struct {
	db                       *mongo.Database
	logger                   *log.Logger
	minActivity, maxActivity time.Duration
}

func nextID() int64 { return atomic.AddInt64(&sequence, 1) }
func (h *harness) event(u *actor, collection, action, detail string, err error) {
	username, tenant := "-", "-"
	if u != nil {
		username, tenant = u.Username, u.TenantID
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	h.logger.Printf("user=%q tenantId=%q collection=%q action=%q result=%q detail=%q error=%q",
		username, tenant, collection, action, result, detail, fmt.Sprint(err))
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return ctx.Err() == nil
	}
}
func (u *actor) delay(min, max time.Duration) time.Duration {
	if min == max {
		return min
	}
	return min + time.Duration(u.rng.Int63n(int64(max-min)+1))
}
func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (runErr error) {
	users := flag.Int("users", 100, "Users to create; 0 clears the database and exits")
	dbName := flag.String("database", os.Getenv("MONGODB_NAME"), "Database to CLEAR and test; defaults to MONGODB_NAME")
	duration := flag.Duration("duration", 0, "Activity duration after setup; 0 runs until Ctrl+C")
	minActivity := flag.Duration("activity-min", time.Second, "Minimum delay between a user's actions")
	maxActivity := flag.Duration("activity-max", 5*time.Second, "Maximum delay between a user's actions")
	flag.Parse()
	if *users > len(firstNames)*len(lastNames) {
		return fmt.Errorf("at most %d unique first.last usernames are available", len(firstNames)*len(lastNames))
	}
	if *users < 0 || *duration < 0 || *minActivity <= 0 || *maxActivity < *minActivity {
		return errors.New("invalid users, duration, or activity interval")
	}
	if strings.TrimSpace(*dbName) == "" {
		return errors.New("set MONGODB_NAME or -database explicitly")
	}
	switch strings.ToLower(*dbName) {
	case "admin", "local", "config":
		return errors.New("MongoDB system databases cannot be test targets")
	}
	// Set before initialization: backend methods use both this environment
	// variable and the database package global.
	if err := os.Setenv("MONGODB_NAME", *dbName); err != nil {
		return err
	}
	f, err := os.OpenFile("scalability-test-v2.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	logger := log.New(f, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	log.SetOutput(f) // Capture backend log.Printf, including metrics failures.
	utils.AuditLog = logger
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		uri, _, err = database.BuildMongoURI()
		if err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	client, err := mongo.Connect(connectCtx, options.Client().ApplyURI(uri))
	if err == nil {
		err = client.Ping(connectCtx, readpref.Primary())
	}
	cancel()
	if client != nil {
		defer func() {
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = client.Disconnect(c)
		}()
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	database.Client = client
	database.Database = *dbName
	database.URI = uri
	database.IsConnected = true
	h := &harness{db: client.Database(*dbName), logger: logger, minActivity: *minActivity, maxActivity: *maxActivity}
	fmt.Printf("Clearing all records in database %q\n", *dbName)
	if err = h.cleanup(ctx); err != nil {
		return err
	}
	actors := make([]*actor, 0, *users)
	started := time.Now()
	// Produce a report even when setup fails or is interrupted.
	defer func() {
		reportErr := h.report(actors, started)
		if reportErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("write report: %w", reportErr))
		}
	}()
	if *users == 0 {
		return nil
	}
	if err = h.seedGlobal(); err != nil {
		return err
	}
	var uc database.UsersCollection
	if err = uc.Init(client); err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	names := rng.Perm(len(firstNames) * len(lastNames))
	fmt.Print("Initializing")
	initializationInProgress := true
	defer func() {
		if initializationInProgress {
			fmt.Println()
		}
	}()
	for i := 0; i < *users; i++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		n := names[i]
		first, last := firstNames[n/len(lastNames)], lastNames[n%len(lastNames)]
		username := strings.ToLower(first + "." + last + "@test.com")
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		tid, addErr := uc.Add(c, model.UserRequest{Username: username, Name: first + " " + last, Password: primitive.NewObjectID().Hex()})
		cancel()
		if addErr != nil {
			h.event(nil, "users", "add", username, addErr)
			return addErr
		}
		u := &actor{Username: username, TenantID: tid, rng: rand.New(rand.NewSource(rng.Int63()))}
		actors = append(actors, u)
		h.event(u, "users", "add", "", nil)
		for _, init := range []func(*mongo.Client) error{u.ac.Init, u.sc.Init, u.oc.Init, u.gc.Init, u.ec.Init} {
			if err = init(client); err != nil {
				return err
			}
		}
		if err = uc.SetSportsSupported(tid, []string{"Basketball", "Softball"}); err != nil {
			return err
		}
		var lc database.LevelsCollection
		if err = lc.Init(client); err != nil {
			return err
		}
		for _, sport := range []string{"Basketball", "Softball"} {
			if err = lc.Add(database.Level{Sport: sport, Name: "Varsity", GameFee: 7500}, tid); err != nil {
				return err
			}
		}
		if (i+1)%100 == 0 {
			fmt.Print(".")
		}
	}
	fmt.Println()
	initializationInProgress = false
	fmt.Printf("Initialized collection wrappers and configuration for %d users.\n", len(actors))
	started = time.Now()
	runCtx, cancelRun := context.WithCancel(ctx)
	if *duration > 0 {
		cancelRun()
		runCtx, cancelRun = context.WithTimeout(ctx, *duration)
	}
	defer cancelRun()
	fmt.Printf("Simulating %d users in %s; Ctrl+C stops and writes the report.\n", len(actors), *dbName)
	var wg sync.WaitGroup
	for _, u := range actors {
		wg.Add(1)
		go func(u *actor) { defer wg.Done(); h.simulate(runCtx, u) }(u)
	}
	wg.Wait()
	return nil
}

func (h *harness) cleanup(ctx context.Context) error {
	names, err := h.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return err
	}
	for _, name := range names {
		if strings.HasPrefix(name, "system.") {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		result, err := h.db.Collection(name).DeleteMany(c, bson.M{})
		cancel()
		h.event(nil, name, "cleanup", "", err)
		if err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
		fmt.Printf("  %s: deleted %d\n", name, result.DeletedCount)
	}
	return nil
}
func (h *harness) seedGlobal() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var sports database.SportsCollection
	if err := sports.Init(database.Client); err != nil {
		return err
	}
	for _, sport := range []string{"Basketball", "Softball"} {
		if err := testcatalog.SeedSport(ctx, h.db, database.Sport{ID: sport, Name: sport, Active: true}); err != nil {
			return err
		}
		titles := []string{"Referee", "U1", "U2", "ECO"}
		if sport == "Softball" {
			titles = []string{"Plate Umpire", "U1", "U3"}
		}
		roles := make([]model.OfficialRole, 0, len(titles))
		for i, title := range titles {
			roles = append(roles, model.OfficialRole{RoleId: fmt.Sprintf("%s-%d", sport, i+1), Title: title, DisplayOrder: i + 1, Active: true})
		}
		if err := testcatalog.SeedOfficialRoles(ctx, h.db, model.OfficialRolesRequest{Sport: sport, Roles: roles}); err != nil {
			return err
		}
	}
	return nil
}
func (h *harness) login(ctx context.Context, u *actor, until time.Time) (string, error) {
	sid := primitive.NewObjectID().Hex()
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := h.db.Collection("sessions").InsertOne(c, model.Session{SessionID: sid, Username: u.Username, TenantID: u.TenantID, Role: "user", ExpiresAt: until})
	h.event(u, "sessions", "login", until.Format(time.RFC3339), err)
	return sid, err
}
func (h *harness) logout(u *actor, sid string) error {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := h.db.Collection("sessions").DeleteOne(c, bson.M{"sessionId": sid, "tenantId": u.TenantID})
	h.event(u, "sessions", "logout", "", err)
	return err
}
func (h *harness) simulate(ctx context.Context, u *actor) {
	initialDelay := u.delay(0, 5*time.Minute)
	h.event(u, "sessions", "initial-delay", initialDelay.String(), nil)
	if !wait(ctx, initialDelay) {
		return
	}
	for ctx.Err() == nil {
		until := time.Now().Add(time.Duration(1+u.rng.Intn(10)) * time.Minute)
		sid, err := h.login(ctx, u, until)
		if err != nil {
			if !wait(ctx, 10*time.Second) {
				return
			}
			continue
		}
		for ctx.Err() == nil && time.Now().Before(until) {
			delay := u.delay(h.minActivity, h.maxActivity)
			if remaining := time.Until(until); delay > remaining {
				delay = remaining
			}
			if !wait(ctx, delay) || !time.Now().Before(until) {
				break
			}
			// One operation at a time per account; different accounts run concurrently.
			// A call admitted before expiry may finish afterward, as with a real request.
			c, cancel := context.WithDeadline(ctx, until)
			h.activity(c, u)
			cancel()
		}
		// Retry logout before allowing another session. Expiry prevents an old
		// session from remaining valid if cleanup fails during shutdown.
		for err = h.logout(u, sid); err != nil && ctx.Err() == nil; err = h.logout(u, sid) {
			if !wait(ctx, 10*time.Second) {
				return
			}
		}
		if !wait(ctx, time.Duration(1+u.rng.Intn(5))*time.Minute) {
			return
		}
	}
}

// Random selection is always tenant-scoped. No-eligible-record is a skipped
// action, not a database error. Independent counters do not increment metrics.
func (h *harness) sample(ctx context.Context, u *actor, collection string, extra bson.M, out interface{}) error {
	filter := bson.M{"tenantId": u.TenantID}
	for k, v := range extra {
		filter[k] = v
	}
	cur, err := h.db.Collection(collection).Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: filter}}, bson.D{{Key: "$sample", Value: bson.M{"size": 1}}},
	})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	if !cur.Next(ctx) {
		if err = cur.Err(); err != nil {
			return err
		}
		return errEmpty
	}
	return cur.Decode(out)
}
func (h *harness) activity(ctx context.Context, u *actor) {
	index := u.rng.Intn(len(collections))
	collection := collections[index]
	action := u.rng.Intn(3)
	// Favor adds so the test builds a useful working set instead of hovering at zero.
	if u.rng.Intn(2) == 0 {
		action = 0
	}
	label := []string{"add", "change", "delete"}[action]
	before := u.Stats[index]
	err := h.modify(ctx, u, index, action)
	if errors.Is(err, errEmpty) {
		h.event(u, collection, label, "skipped: no eligible records", nil)
		return
	}
	if err != nil {
		u.Stats[index].Errors++
		h.event(u, collection, label, "", err)
		return
	}
	// Payment methods provide their own success counts, including partial success.
	if index != 5 {
		switch action {
		case 0:
			u.Stats[index].Adds++
		case 1:
			u.Stats[index].Changes++
		case 2:
			u.Stats[index].Deletes++
		}
	}
	h.event(u, collection, label, fmt.Sprintf("before=%s after=%s", before.String(), u.Stats[index].String()), nil)
}

func (h *harness) modify(ctx context.Context, u *actor, index, action int) error {
	id := nextID()
	tag := fmt.Sprintf("test-%d", id)
	contact := firstNames[u.rng.Intn(len(firstNames))] + " " + lastNames[u.rng.Intn(len(lastNames))]
	switch index {
	case 0:
		a := database.Association{Id: tag, Name: fmt.Sprintf("Georgia Sports Association %d", id), Contact: contact, Phone: "770-555-0100", Email: "assignor@test.com", Assignors: contact}
		if action == 0 {
			return u.ac.Add(a, u.TenantID)
		}
		var doc database.AssociationDoc
		if err := h.sample(ctx, u, "associations", nil, &doc); err != nil {
			return err
		}
		if action == 2 {
			// Do not orphan records that use this association.
			for _, coll := range []string{"games", "payments", "expenses"} {
				n, err := h.db.Collection(coll).CountDocuments(ctx, bson.M{"tenantId": u.TenantID, "association": doc.Id})
				if err != nil {
					return err
				}
				if n > 0 {
					return errEmpty
				}
			}
			return u.ac.Delete(doc.Id, u.TenantID, doc.Version)
		}
		a = database.Association{Version: doc.Version, Id: doc.Id, Name: doc.Name, Contact: tag, Phone: doc.Phone, Email: doc.Email, Assignors: doc.Assignors}
		return u.ac.Update(a, u.TenantID)
	case 4:
		s := database.Site{Name: fmt.Sprintf("Georgia Park %d", id), Contact: contact, Phone: "770-555-0101", Email: "site@test.com"}
		if action == 0 {
			return u.sc.Add(s, u.TenantID)
		}
		var doc database.SiteDoc
		if err := h.sample(ctx, u, "sites", nil, &doc); err != nil {
			return err
		}
		if action == 2 {
			n, err := h.db.Collection("games").CountDocuments(ctx, bson.M{"tenantId": u.TenantID, "site": doc.Name})
			if err != nil {
				return err
			}
			if n > 0 {
				return errEmpty
			}
			return u.sc.Delete(doc.Name, u.TenantID, doc.Version)
		}
		s = database.Site{Version: doc.Version, Name: doc.Name, Contact: tag, Phone: doc.Phone, Email: doc.Email}
		return u.sc.Update(s, u.TenantID)
	case 3:
		o := database.Official{FirstName: strings.Split(contact, " ")[0], LastName: fmt.Sprintf("%s%d", strings.Split(contact, " ")[1], id), Phone: "770-555-0102", Email: tag + "@test.com", Address: "100 Test Street"}
		if action == 0 {
			return u.oc.Add(o, u.TenantID)
		}
		var doc database.OfficialDoc
		if err := h.sample(ctx, u, "officials", nil, &doc); err != nil {
			return err
		}
		if action == 2 {
			return u.oc.Delete(doc.FirstName, doc.LastName, u.TenantID, doc.Version)
		}
		o = database.Official{Version: doc.Version, FirstName: doc.FirstName, LastName: doc.LastName, Phone: doc.Phone, Email: doc.Email, Address: tag}
		return u.oc.Update(u.TenantID, o)
	case 1:
		if action == 0 {
			var a database.AssociationDoc
			if err := h.sample(ctx, u, "associations", nil, &a); err != nil {
				return err
			}
			types := []string{"Camp Fee", "Dues", "Equipment", "Food", "Mileage"}
			e := database.Expense{Date: time.Now().Format("2006-01-02"), Type: types[u.rng.Intn(len(types))], Amount: money(id + 1000), Association: a.Id, GameId: "0", Description: tag, TaxYear: time.Now().Format("2006")}
			// Backend expects mileage in Amount, and stores hundredths of a mile.
			if e.Type == "Mileage" {
				e.Amount = fmt.Sprint(id + 1)
			}
			return u.ec.Add(e, u.TenantID)
		}
		var doc database.ExpenseDoc
		if err := h.sample(ctx, u, "expenses", nil, &doc); err != nil {
			return err
		}
		if action == 2 {
			return u.ec.Delete(doc.ExpenseId, doc.Association, u.TenantID, doc.Version)
		}
		dt, err := time.Parse("1/2/2006", doc.Date)
		if err != nil {
			return err
		}
		amount := money(doc.Amount)
		if doc.Type == "Mileage" {
			amount = money(doc.Mileage)
		}
		// Preserve the sampled type while changing the description.
		return u.ec.Update(database.Expense{Version: doc.Version, Date: dt.Format("2006-01-02"), Type: doc.Type, Amount: amount, Association: doc.Association, Description: tag, TaxYear: doc.TaxYear}, doc.ExpenseId, doc.Association, u.TenantID)
	case 2:
		if action == 0 {
			var a database.AssociationDoc
			var site database.SiteDoc
			var official database.OfficialDoc
			if err := h.sample(ctx, u, "associations", nil, &a); err != nil {
				return err
			}
			if err := h.sample(ctx, u, "sites", nil, &site); err != nil {
				return err
			}
			if err := h.sample(ctx, u, "officials", nil, &official); err != nil {
				return err
			}
			sport := "Basketball"
			if u.rng.Intn(2) == 0 {
				sport = "Softball"
			}
			roles, err := activeOfficialRoles(sport)
			if err != nil {
				return err
			}
			assignments := make([]model.GameOfficialDoc, 0, len(roles.Roles))
			officialID := official.OfficialId
			if officialID == 0 {
				officialID = official.Id
			}
			for i, role := range roles.Roles {
				assignment := model.GameOfficialDoc{RoleId: role.RoleId, Title: role.Title, Name: "Unassigned"}
				if i == 0 {
					assignment.OfficialId = fmt.Sprint(officialID)
					assignment.Name = official.FirstName + " " + official.LastName
				}
				assignments = append(assignments, assignment)
			}
			date := time.Now().AddDate(0, 0, 1+u.rng.Intn(90))
			g := model.GameDescriptor{GameId: fmt.Sprint(id), Date: date.Format("1/2/2006"), Time: fmt.Sprintf("%d:%02d PM", 1+u.rng.Intn(10), u.rng.Intn(60)), Sport: sport, Site: site.Name, Field: "1", NumOfGames: "1", Level: "Varsity", Home: "Home School", Visitor: "Visitor School", GameFee: money(int64(4500 + u.rng.Intn(5001))), TravelPay: "0.00", AssignorFee: "0.00", Deductions: "0.00", Association: a.Id, Status: "Pending", Assignor: strings.TrimSpace(strings.Split(a.Assignors, ",")[0]), TaxYear: date.Format("2006"), Notes: tag, Officials: assignments}
			return u.gc.Add(u.TenantID, g)
		}
		var doc model.GameDoc
		if err := h.sample(ctx, u, "games", bson.M{"status": bson.M{"$ne": "Paid"}}, &doc); err != nil {
			return err
		}
		if action == 2 {
			n, err := h.db.Collection("payments").CountDocuments(ctx, bson.M{"tenantId": u.TenantID, "association": doc.Association, "gameIds": doc.GameId})
			if err != nil {
				return err
			}
			if n > 0 {
				return errEmpty
			}
			return u.gc.Delete(doc.Association, fmt.Sprint(doc.GameId), u.TenantID, doc.Version)
		}
		g := u.gc.ConvDocToGame(doc)
		g.Notes = tag
		g.Status = "Completed"
		return database.UpdateOneGameDoc(ctx, g, h.db.Name(), "games", u.TenantID)
	case 5:
		var p model.PaymentDescriptor
		if action == 0 {
			var game model.GameDoc
			if err := h.sample(ctx, u, "games", bson.M{"status": "Completed"}, &game); err != nil {
				return err
			}
			// Check payments too: an earlier partially successful payment may
			// have been inserted without marking this game Paid.
			n, err := h.db.Collection("payments").CountDocuments(ctx, bson.M{"tenantId": u.TenantID, "association": game.Association, "gameIds": game.GameId})
			if err != nil {
				return err
			}
			if n > 0 {
				return errEmpty
			}
			total, err := database.GetGameFee([]int64{game.GameId}, u.TenantID, "", game.Association)
			if err != nil {
				return err
			}
			p = model.PaymentDescriptor{PaymentId: tag, PaymentDate: time.Now().Format("1/2/2006"), PaymentAmt: money(total), Association: game.Association, GameIds: fmt.Sprint(game.GameId), TaxYear: time.Now().Format("2006")}
		} else {
			var doc model.PaymentDoc
			if err := h.sample(ctx, u, "payments", nil, &doc); err != nil {
				return err
			}
			if action == 2 {
				// DeletePayment returns referenced games to Completed.
				before, err := h.db.Collection("games").CountDocuments(ctx, bson.M{"tenantId": u.TenantID, "association": doc.Association, "gameId": bson.M{"$in": doc.GameIds}, "status": bson.M{"$ne": "Completed"}})
				if err != nil {
					return err
				}
				err = database.DeletePayment(doc.PaymentId, doc.Association, u.TenantID, doc.Version)
				if err == nil {
					u.Stats[5].Deletes++
					u.Stats[2].Changes += before
				}
				return err
			}
			ids := make([]string, len(doc.GameIds))
			for i, n := range doc.GameIds {
				ids[i] = fmt.Sprint(n)
			}
			date, err := time.Parse("1/2/2006", doc.PaymentDate)
			if err != nil {
				return err
			}
			// Advance the date to guarantee a real change; preserve game IDs.
			p = model.PaymentDescriptor{Version: doc.Version, PaymentId: doc.PaymentId, PaymentDate: date.AddDate(0, 0, 1).Format("1/2/2006"), PaymentAmt: money(doc.PaymentAmt), Association: doc.Association, GameIds: strings.Join(ids, ","), TaxYear: fmt.Sprint(doc.TaxYear)}
		}
		var success, failures, games int
		var errs []error
		if action == 0 {
			success, failures, games, errs = database.InsertPaymentDocs(ctx, []model.PaymentDescriptor{p}, h.db.Name(), "payments", u.TenantID)
			u.Stats[5].Adds += int64(success)
		} else {
			success, failures, games, errs = database.UpdatePaymentDocs(ctx, []model.PaymentDescriptor{p}, h.db.Name(), "payments", u.TenantID)
			u.Stats[5].Changes += int64(success)
		}
		u.Stats[2].Changes += int64(games)
		if failures > 0 || len(errs) > 0 {
			return fmt.Errorf("payment: saved=%d gamesUpdated=%d failures=%d: %v", success, games, failures, errors.Join(errs...))
		}
		if success != 1 {
			return errors.New("payment backend returned no successful write")
		}
		return nil
	}
	return errors.New("unknown collection")
}

func (h *harness) report(users []*actor, started time.Time) error {
	f, err := os.Create("scalability-test-v2.rpt")
	if err != nil {
		return err
	}
	defer f.Close()
	w := tabwriter.NewWriter(io.MultiWriter(os.Stdout, f), 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Scalability Test V2\nDatabase: %s\nStarted: %s\nFinished: %s\n", h.db.Name(), started.Format(time.RFC3339), time.Now().Format(time.RFC3339))
	fmt.Fprintln(w, "Cells: successful adds/changes/deletions/errors; skipped actions excluded.")
	fmt.Fprintln(w, "Game changes include payment status side effects; setup users/levels excluded.")
	fmt.Fprintln(w, "User\tTenant ID\tAssociations\tExpenses\tGames\tOfficials\tSites\tPayments")
	var totals [6]counts
	for _, u := range users {
		fmt.Fprintf(w, "%s\t%s", u.Username, u.TenantID)
		for i, c := range u.Stats {
			fmt.Fprintf(w, "\t%s", c.String())
			totals[i].Adds += c.Adds
			totals[i].Changes += c.Changes
			totals[i].Deletes += c.Deletes
			totals[i].Errors += c.Errors
		}
		fmt.Fprintln(w)
	}
	fmt.Fprint(w, "TOTAL\t-")
	for _, c := range totals {
		fmt.Fprintf(w, "\t%s", c.String())
	}
	fmt.Fprintln(w)
	var grand counts
	for _, c := range totals {
		grand.Adds += c.Adds
		grand.Changes += c.Changes
		grand.Deletes += c.Deletes
		grand.Errors += c.Errors
	}
	fmt.Fprintf(w, "Total Adds: %d Total Changes: %d Total Deletes: %d  Total Errors: %d\n",
		grand.Adds, grand.Changes, grand.Deletes, grand.Errors)
	return w.Flush()
}

var firstNames = []string{
	"James", "Mary", "John", "Patricia", "Robert",
	"Jennifer", "Michael", "Linda", "William", "Elizabeth",
	"David", "Barbara", "Richard", "Susan", "Joseph",
	"Jessica", "Thomas", "Sarah", "Charles", "Karen",
	"Christopher", "Nancy", "Daniel", "Lisa", "Matthew",
	"Betty", "Anthony", "Margaret", "Mark", "Sandra",
	"Donald", "Ashley", "Steven", "Kimberly", "Paul",
	"Emily", "Andrew", "Donna", "Joshua", "Michelle",
	"Kenneth", "Carol", "Kevin", "Amanda", "Brian",
	"Dorothy", "George", "Melissa", "Timothy", "Deborah",
	"Ronald", "Stephanie", "Jason", "Rebecca", "Edward",
	"Sharon", "Jeffrey", "Laura", "Ryan", "Cynthia",
	"Jacob", "Kathleen", "Gary", "Amy", "Nicholas",
	"Angela", "Eric", "Shirley", "Jonathan", "Anna",
	"Stephen", "Brenda", "Larry", "Pamela", "Justin",
	"Emma", "Scott", "Nicole", "Brandon", "Helen",
	"Benjamin", "Samantha", "Samuel", "Katherine", "Gregory",
	"Christine", "Frank", "Debra", "Alexander", "Rachel",
	"Raymond", "Carolyn", "Patrick", "Janet", "Jack",
	"Catherine", "Dennis", "Maria", "Jerry", "Heather",
}

var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones",
	"Garcia", "Miller", "Davis", "Rodriguez", "Martinez",
	"Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson",
	"Thomas", "Taylor", "Moore", "Jackson", "Martin",
	"Lee", "Perez", "Thompson", "White", "Harris",
	"Sanchez", "Clark", "Ramirez", "Lewis", "Robinson",
	"Walker", "Young", "Allen", "King", "Wright",
	"Scott", "Torres", "Nguyen", "Hill", "Flores",
	"Green", "Adams", "Nelson", "Baker", "Hall",
	"Rivera", "Campbell", "Mitchell", "Carter", "Roberts",
	"Gomez", "Phillips", "Evans", "Turner", "Diaz",
	"Parker", "Cruz", "Edwards", "Collins", "Reyes",
	"Stewart", "Morris", "Morales", "Murphy", "Cook",
	"Rogers", "Gutierrez", "Ortiz", "Morgan", "Cooper",
	"Peterson", "Bailey", "Reed", "Kelly", "Howard",
	"Ramos", "Kim", "Cox", "Ward", "Richardson",
	"Watson", "Brooks", "Chavez", "Wood", "James",
	"Bennett", "Gray", "Mendoza", "Ruiz", "Hughes",
	"Price", "Alvarez", "Castillo", "Sanders", "Patel",
	"Myers", "Long", "Ross", "Foster", "Jimenez",
}

func activeOfficialRoles(sport string) (model.OfficialRolesRequest, error) {
	request, err := database.GetOfficialRoles(sport)
	if err != nil {
		return request, err
	}
	return testcatalog.ActiveRoles(request), nil
}
