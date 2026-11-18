// Save as main.go inside your ref-ledger-v2 module (for example tests/metrics/metrics-test).
// List options without connecting or changing data: go run ./tests/metrics/metrics-test -options
// Run: go run ./tests/metrics/metrics-test -records 1000
// Run continuously: go run ./tests/metrics/metrics-test -records -1
// Optional: -interval 100ms -delete-percent 20 -update-percent 40
// Ctrl+C stops adding, then drains queued jobs before exiting. Existing database
// methods may finish their current operation before shutdown completes.
// STARTUP: deletes games for selected users' tenants and ALL metrics records.
// -tenants id1,id2 limits selection; omitted means all distinct tenants in users.
// -records is PER TENANT. Each tenant has its own add, update, and delete goroutines.
// After startup cleanup, only games added by THIS run are updated or deleted.
// A bounded pool keeps at most -pool recent/random retained candidates in memory;
// evicting a candidate from the pool does NOT delete its database record.
// Tenants missing sites, associations, levels, or roles for -sport are skipped.
// Skipped tenants keep their games. If none qualify, no cleanup runs.
// -records 0 remains cleanup-only for all selected tenants (no configuration check).
// Run one test process at a time; startup cleanup and game IDs are not coordinated
// with other test processes or application writers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type candidate struct {
	mu      sync.Mutex
	game    model.GameDescriptor
	updates uint64
	busy    bool
	deleted bool
}
type totals struct{ added, updated, deleted, failed int64 }
type environment struct {
	levels           []database.Level
	officials        []database.Official
	sites            []database.SiteName
	associations     []database.Association
	roles            model.OfficialRolesRequest
	addCollection    database.GameCollection
	deleteCollection database.GameCollection
}

var errIncompleteConfiguration = errors.New("incomplete tenant configuration")

func initialize(tenant, sport string) (*environment, error) {
	if err := database.Connect(); err != nil {
		return nil, err
	}
	e := &environment{}
	// Separate collection wrappers: Add and Delete both write LastError.
	if err := e.addCollection.Init(database.Client); err != nil {
		return nil, err
	}
	if err := e.deleteCollection.Init(database.Client); err != nil {
		return nil, err
	}
	var lc database.LevelsCollection
	var oc database.OfficialCollection
	var sc database.SiteCollection
	var ac database.AssociationCollection
	if err := lc.Init(database.Client); err != nil {
		return nil, err
	}
	if err := oc.Init(database.Client); err != nil {
		return nil, err
	}
	if err := sc.Init(database.Client); err != nil {
		return nil, err
	}
	if err := ac.Init(database.Client); err != nil {
		return nil, err
	}
	var err error
	e.levels, err = lc.GetLevels(tenant, true, sport)
	if err != nil {
		return nil, err
	}
	e.officials, err = oc.GetOfficialsDirectory("", "", tenant)
	if err != nil {
		return nil, err
	}
	e.sites, err = sc.GetSiteNames(tenant)
	if err != nil {
		return nil, err
	}
	e.associations, err = ac.GetAssociationsDirectory(tenant)
	if err != nil {
		return nil, err
	}
	e.roles, err = database.GetOfficialRoles(sport)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	if len(e.levels) == 0 {
		missing = append(missing, "levels")
	}
	if len(e.sites) == 0 {
		missing = append(missing, "sites")
	}
	if len(e.associations) == 0 {
		missing = append(missing, "associations")
	}
	if len(e.roles.Roles) == 0 {
		missing = append(missing, "official roles")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: missing %s for sport %s", errIncompleteConfiguration, strings.Join(missing, ", "), sport)
	}
	return e, nil
}

// resetTestData deletes games only for the specified tenant before workers start.
// Direct deletes keep this cleanup out of the new test's metrics.
func resetTestData(parent context.Context, tenant string) error {
	if strings.TrimSpace(tenant) == "" || tenant == "na" {
		return fmt.Errorf("valid tenant ID required for cleanup")
	}
	db := database.Client.Database(database.Database)
	fmt.Printf("Startup cleanup: database=%s tenant=%s games\n", database.Database, tenant)
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	games, err := db.Collection("games").DeleteMany(ctx, bson.M{"tenantId": tenant})
	if err != nil {
		return fmt.Errorf("delete tenant games: %w", err)
	}
	fmt.Printf("Startup cleanup: deleted %d games for tenant %s\n", games.DeletedCount, tenant)

	return nil
}

var schools = []string{
	"Brookwood High School", "Parkview High School", "Grayson High School",
	"Collins Hill High School", "Mill Creek High School", "North Gwinnett High School",
	"Buford High School", "Norcross High School", "Peachtree Ridge High School",
	"Archer High School", "Marietta High School", "Walton High School",
	"Lassiter High School", "Pope High School", "Milton High School",
	"Alpharetta High School", "Roswell High School", "Valdosta High School",
	"Lowndes High School", "Camden County High School",
}

func (e *environment) generate(rng *rand.Rand, id int64, sport string, year int) model.GameDescriptor {
	home := rng.Intn(len(schools))
	visitor := rng.Intn(len(schools) - 1)
	if visitor >= home {
		visitor++
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	days := int(start.AddDate(1, 0, 0).Sub(start).Hours() / 24)
	date := start.AddDate(0, 0, rng.Intn(days)).Format("1/2/2006")
	clock := start.Add(time.Duration(rng.Intn(24*60)) * time.Minute).Format("3:04 PM")
	association := e.associations[rng.Intn(len(e.associations))]
	assignors := []string{}
	for _, name := range strings.Split(association.Assignors, ",") {
		if name = strings.TrimSpace(name); name != "" {
			assignors = append(assignors, name)
		}
	}
	assignor := "Unassigned"
	if len(assignors) > 0 {
		assignor = assignors[rng.Intn(len(assignors))]
	}
	assignments := make([]model.GameOfficialDoc, 0, 4)
	for _, role := range e.roles.Roles {
		if !role.Active {
			continue
		}
		if len(assignments) >= 4 || len(assignments) >= len(e.officials) {
			break
		}
		official := e.officials[len(assignments)]
		assignments = append(assignments, model.GameOfficialDoc{
			RoleId: role.RoleId, Title: role.Title, DisplayOrder: role.DisplayOrder,
			OfficialId: strconv.FormatInt(official.Id, 10),
			Name:       strings.TrimSpace(official.FirstName + " " + official.LastName),
		})
	}
	gameID := strconv.FormatInt(id, 10)
	return model.GameDescriptor{
		GameId: gameID, Officials: assignments, Date: date, Time: clock, Sport: sport,
		Site: e.sites[rng.Intn(len(e.sites))].Name, Field: "1",
		Home: schools[home], Visitor: schools[visitor], Status: "Pending",
		Level: e.levels[rng.Intn(len(e.levels))].Name, NumOfGames: "1",
		GameFee: "95.00", TravelPay: "20.00", AssignorFee: "0.00",
		Association: association.Id, Assignor: assignor, TaxYear: strconv.Itoa(year),
		Notes: fmt.Sprintf("Game Id %s added.", gameID),
	}
}

// The main goroutine alone owns the pool slice. Each entry's state is locked.
// A busy entry cannot be selected by either worker until its job finishes.
func dispatch(rng *rand.Rand, pool []*candidate, jobs chan<- *candidate) {
	if len(pool) == 0 {
		return
	}
	start := rng.Intn(len(pool))
	for offset := 0; offset < len(pool); offset++ {
		c := pool[(start+offset)%len(pool)]
		c.mu.Lock()
		if c.busy || c.deleted {
			c.mu.Unlock()
			continue
		}
		c.busy = true
		c.mu.Unlock()
		// Bound pending work; skip a selection if this worker's queue is full.
		select {
		case jobs <- c:
		default:
			c.mu.Lock()
			c.busy = false
			c.mu.Unlock()
		}
		return
	}
}

func deleteWorker(e *environment, tenant string, jobs <-chan *candidate, stats *totals, wg *sync.WaitGroup) {
	defer wg.Done()
	for c := range jobs {
		c.mu.Lock()
		game := c.game
		c.mu.Unlock()
		fresh, loadErr := database.GetSingleGame(context.Background(), game.GameId, game.Association, tenant)
		if loadErr == nil {
			game.Version = fresh.Version
		}
		err := e.deleteCollection.Delete(game.Association, game.GameId, tenant, game.Version)
		c.mu.Lock()
		if err == nil {
			c.deleted = true
			stats.deleted++
		} else {
			stats.failed++
		}
		c.busy = false
		c.mu.Unlock()
		if err != nil {
			log.Printf("delete game %s/%s: %v", game.Association, game.GameId, err)
		}
	}
}

func updateWorker(tenant string, jobs <-chan *candidate, stats *totals, wg *sync.WaitGroup) {
	defer wg.Done()
	for c := range jobs {
		c.mu.Lock()
		game := c.game
		next := c.updates + 1
		c.mu.Unlock()
		fresh, loadErr := database.GetSingleGame(context.Background(), game.GameId, game.Association, tenant)
		if loadErr == nil {
			game.Version = fresh.Version
		}
		game.Notes = fmt.Sprintf("Game Id %s added. This record has been updated %d times.", game.GameId, next)
		// Uses the existing application update path, including its metrics call.
		err := database.UpdateOneGameDoc(context.Background(), game, database.Database, "games", tenant)
		c.mu.Lock()
		if err == nil {
			c.game = game
			c.updates = next
			stats.updated++
		} else {
			stats.failed++
		}
		c.busy = false
		c.mu.Unlock()
		if err != nil {
			log.Printf("update game %s/%s: %v", game.Association, game.GameId, err)
		}
	}
}

// loadTenants reads only tenantId; password hashes and other user fields are not loaded.
func loadTenants(ctx context.Context, selection string) ([]string, error) {
	filter := bson.M{}
	requested := map[string]bool{}
	if strings.TrimSpace(selection) != "" {
		ids := []string{}
		for _, id := range strings.Split(selection, ",") {
			id = strings.TrimSpace(id)
			if id == "" || id == "na" {
				return nil, fmt.Errorf("invalid tenant selection")
			}
			if !requested[id] {
				requested[id] = true
				ids = append(ids, id)
			}
		}
		filter["tenantId"] = bson.M{"$in": ids}
	}
	cursor, err := database.Client.Database(database.Database).Collection("users").Find(
		ctx, filter, options.Find().SetProjection(bson.M{"_id": 0, "tenantId": 1}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	seen := map[string]bool{}
	tenants := []string{}
	for cursor.Next(ctx) {
		var user struct {
			TenantID string `bson:"tenantId"`
		}
		if err := cursor.Decode(&user); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(user.TenantID)
		if id == "" || id == "na" || seen[id] {
			continue
		}
		seen[id] = true
		tenants = append(tenants, id)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	for id := range requested {
		if !seen[id] {
			return nil, fmt.Errorf("tenant %s was not found in users", id)
		}
	}
	if len(tenants) == 0 {
		return nil, fmt.Errorf("no valid tenant IDs found in users")
	}
	sort.Strings(tenants)
	return tenants, nil
}

func runTenant(ctx context.Context, e *environment, tenant, sport string, records int64, year int, interval time.Duration, deletePercent, updatePercent, poolSize int) error {
	// Startup cleanup has removed this tenant's games, so IDs begin at 1.
	var id int64
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	deletes := make(chan *candidate, 64)
	updates := make(chan *candidate, 64)
	var wg sync.WaitGroup
	var addStats, deleteStats, updateStats totals
	wg.Add(2)
	go deleteWorker(e, tenant, deletes, &deleteStats, &wg)
	go updateWorker(tenant, updates, &updateStats, &wg)
	pool := make([]*candidate, 0, poolSize)
	var runErr error
	for records == -1 || addStats.added < records {
		if ctx.Err() != nil {
			break
		}
		if id == math.MaxInt64 {
			runErr = fmt.Errorf("game ID space exhausted")
			break
		}
		id++
		game := e.generate(rng, id, sport, year)
		// Add returns an error, unlike InsertGameDocs, so failed inserts are never queued.
		// On failure stop rather than loop forever against a misconfigured database.
		if err := e.addCollection.Add(tenant, game); err != nil {
			addStats.failed++
			runErr = fmt.Errorf("add game %s: %w", game.GameId, err)
			break
		}
		addStats.added++
		c := &candidate{game: game}
		if len(pool) < poolSize {
			pool = append(pool, c)
		} else {
			// Replace an idle candidate. Workers retain their own pointers to busy jobs.
			start := rng.Intn(len(pool))
			for offset := 0; offset < len(pool); offset++ {
				index := (start + offset) % len(pool)
				old := pool[index]
				old.mu.Lock()
				idle := !old.busy
				old.mu.Unlock()
				if idle {
					pool[index] = c
					break
				}
			}
		}
		// Independent random decisions: both kinds of job may be sent per addition.
		// A game can be updated repeatedly, then selected for deletion later.
		if rng.Intn(100) < updatePercent {
			dispatch(rng, pool, updates)
		}
		if rng.Intn(100) < deletePercent {
			dispatch(rng, pool, deletes)
		}
		if interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	fmt.Printf("Tenant %s: stopping additions; waiting for queued jobs...\n", tenant)
	close(updates)
	close(deletes)
	wg.Wait()
	fmt.Printf("Tenant %s finished: added=%d updated=%d deleted=%d errors=%d\n", tenant, addStats.added, updateStats.updated, deleteStats.deleted, addStats.failed+updateStats.failed+deleteStats.failed)
	return runErr
}

func run() error {
	records := flag.Int64("records", 1000, "successful records to add PER TENANT; -1 runs until Ctrl+C; 0 performs cleanup only")
	selection := flag.String("tenants", "", "comma-separated tenant IDs from users; blank selects all users' tenants")
	sport := flag.String("sport", "Basketball", "sport to generate for every selected tenant")
	year := flag.Int("year", 2026, "game date and tax year")
	interval := flag.Duration("interval", 0, "pause between additions per tenant, e.g. 100ms")
	deletePercent := flag.Int("delete-percent", 17, "delete selection chance per addition (0-100)")
	updatePercent := flag.Int("update-percent", 17, "update selection chance per addition (0-100)")
	poolSize := flag.Int("pool", 10000, "maximum tracked candidates PER TENANT")
	dumpOptions := flag.Bool("options", false, "print permitted command-line options and exit without database access")
	flag.Parse()
	if *dumpOptions {
		flag.CommandLine.SetOutput(os.Stdout)
		fmt.Printf("Usage: %s [options]\n\nPermitted options:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Println("\n  -h, -help\n        print usage and exit")
		return nil
	}
	if flag.NArg() != 0 {
		return fmt.Errorf("use -records N and optional -tenants id1,id2")
	}
	if *records < -1 || *poolSize < 1 || *interval < 0 || *deletePercent < 0 || *deletePercent > 100 || *updatePercent < 0 || *updatePercent > 100 {
		return fmt.Errorf("invalid record count, pool size, interval, or percentage")
	}
	if *year < 1000 || *year > 9999 {
		return fmt.Errorf("year must have four digits")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := database.Connect(); err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := database.Client.Disconnect(shutdown); err != nil {
			log.Printf("disconnect: %v", err)
		}
	}()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	tenants, err := loadTenants(queryCtx, *selection)
	cancel()
	if err != nil {
		return fmt.Errorf("load tenants: %w", err)
	}
	fmt.Printf("Database=%s tenants=%v records per tenant=%d\n", database.Database, tenants, *records)
	// Initialize all tenants BEFORE any cleanup or goroutines. Database.Connect
	// is already complete, and collection wrapper LastError fields remain private.
	environments := make([]*environment, 0, len(tenants))
	if *records != 0 {
		readyTenants := make([]string, 0, len(tenants))
		skipped := 0
		for _, tenant := range tenants {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			e, initErr := initialize(tenant, *sport)
			if errors.Is(initErr, errIncompleteConfiguration) {
				log.Printf("Skipping tenant %s: %v", tenant, initErr)
				skipped++
				continue
			}
			if initErr != nil {
				return fmt.Errorf("initialize tenant %s: %w", tenant, initErr)
			}
			readyTenants = append(readyTenants, tenant)
			environments = append(environments, e)
		}
		tenants = readyTenants
		fmt.Printf("Tenant configuration: ready=%d skipped=%d\n", len(tenants), skipped)
		if len(tenants) == 0 {
			log.Print("No tenants have complete configuration; no cleanup or test operations performed.")
			return nil
		}
	}
	// Clear participating tenants' games first, then clear metrics exactly once.
	// No workers run until cleanup has finished for all tenants.
	for _, tenant := range tenants {
		if err := resetTestData(ctx, tenant); err != nil {
			return err
		}
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 60*time.Second)
	result, err := database.Client.Database(database.Database).Collection("metrics").DeleteMany(cleanupCtx, bson.M{})
	cleanupCancel()
	if err != nil {
		return fmt.Errorf("delete all metrics after game cleanup: %w", err)
	}
	fmt.Printf("Startup cleanup: deleted %d metrics records across all tenants\n", result.DeletedCount)
	if *records == 0 {
		return nil
	}
	var tenantWorkers sync.WaitGroup
	failures := make(chan error, len(tenants))
	for i, tenant := range tenants {
		e := environments[i]
		tenantWorkers.Add(1)
		go func(tenant string, e *environment) {
			defer tenantWorkers.Done()
			if err := runTenant(ctx, e, tenant, *sport, *records, *year, *interval, *deletePercent, *updatePercent, *poolSize); err != nil {
				failures <- fmt.Errorf("tenant %s: %w", tenant, err)
				// Stop other producers on an insertion failure; queued work still drains.
				stop()
			}
		}(tenant, e)
	}
	tenantWorkers.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func main() {
	utils.AuditLog = log.New(os.Stderr, "AUDIT: ", log.LstdFlags)
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
