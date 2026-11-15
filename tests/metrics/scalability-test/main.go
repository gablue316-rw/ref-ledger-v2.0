package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/model"
	"ref-ledger-v2/internal/utils"
	"ref-ledger-v2/tests/internal/testcatalog"
)

var uc database.UsersCollection
var ac database.AssociationCollection
var sc database.SiteCollection
var oc database.OfficialCollection
var lc database.LevelsCollection
var sp database.SportsCollection

var client *mongo.Client
var db *mongo.Database

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

var sports = []string{
	"Basketball", "Softball",
}

var highSchools = []string{
	"Archer High School",
	"Berkmar High School",
	"Brookwood High School",
	"Central Gwinnett High School",
	"Collins Hill High School",
	"Dacula High School",
	"Discovery High School",
	"Duluth High School",
	"Grayson High School",
	"Gwinnett School of Mathematics, Science, and Technology",
	"Lanier High School",
	"Meadowcreek High School",
	"Mill Creek High School",
	"Mountain View High School",
	"Norcross High School",
	"North Gwinnett High School",
	"Parkview High School",
	"Peachtree Ridge High School",
	"Seckinger High School",
	"Shiloh High School",
	"South Gwinnett High School",
	"Buford High School",
	"Allatoona High School",
	"Campbell High School",
	"Harrison High School",
	"Hillgrove High School",
	"Kell High School",
	"Kennesaw Mountain High School",
	"Lassiter High School",
	"McEachern High School",
	"North Cobb High School",
	"Osborne High School",
	"Pebblebrook High School",
	"Pope High School",
	"South Cobb High School",
	"Sprayberry High School",
	"Walton High School",
	"Wheeler High School",
	"Marietta High School",
	"Alpharetta High School",
	"Banneker High School",
	"Cambridge High School",
	"Centennial High School",
	"Chattahoochee High School",
	"Creekside High School",
	"Johns Creek High School",
	"Langston Hughes High School",
	"Milton High School",
	"North Springs High School",
	"Northview High School",
	"Riverwood International Charter School",
	"Roswell High School",
	"Tri-Cities High School",
	"Westlake High School",
	"Chamblee High School",
	"Clarkston High School",
	"Columbia High School",
	"Cross Keys High School",
	"Druid Hills High School",
	"Dunwoody High School",
	"Lakeside High School",
	"Lithonia High School",
	"Martin Luther King Jr. High School",
	"McNair High School",
	"Miller Grove High School",
	"Redan High School",
	"Southwest DeKalb High School",
	"Stephenson High School",
	"Stone Mountain High School",
	"Towers High School",
	"Tucker High School",
	"Decatur High School",
	"Denmark High School",
	"East Forsyth High School",
	"Forsyth Central High School",
	"Lambert High School",
	"North Forsyth High School",
	"South Forsyth High School",
	"West Forsyth High School",
	"Cherokee High School",
	"Creekview High School",
	"Etowah High School",
	"River Ridge High School",
	"Sequoyah High School",
	"Woodstock High School",
	"East Paulding High School",
	"Hiram High School",
	"North Paulding High School",
	"Paulding County High School",
	"South Paulding High School",
	"Alexander High School",
	"Chapel Hill High School",
	"Douglas County High School",
	"Lithia Springs High School",
	"New Manchester High School",
	"East Coweta High School",
	"Newnan High School",
	"Northgate High School",
	"McIntosh High School",
	"Starr's Mill High School",
}

var locations = []string{
	// Georgia parks with softball fields.
	"Bethesda Park",
	"George Pierce Park",
	"Lenora Park",
	"Dacula Park",
	"Best Friend Park",
	"Bogan Park",
	"Centennial Olympic Park",
	"Chastain Park",

	// Georgia high schools.
	"Brookwood High School",
	"Parkview High School",
	"Grayson High School",
	"Archer High School",
	"Dacula High School",
	"Mill Creek High School",
	"North Gwinnett High School",
	"Peachtree Ridge High School",
	"Collins Hill High School",
	"Mountain View High School",
	"South Gwinnett High School",
	"Central Gwinnett High School",
	"Norcross High School",
	"Duluth High School",
	"Berkmar High School",
}

var levels = []string{
	"SP",
	"JV",
	"Varsity",
	"Minor",
	"Major",
	"Senior",
	"Junior",
	"TB",
	"PW",
	"PW+",
	"TRN30",
	"TRN45",
	"TRN50",
}

// SimulatedUser.mu serializes writes with login/logout for this account.
type SimulatedUser struct {
	mu       sync.Mutex
	TenantID string
	Username string
	online   bool
}

// LoginQueue broadcasts login events so every waiting worker can wake up.
type LoginQueue struct {
	users   []*SimulatedUser // Populated before workers start; immutable afterward.
	mu      sync.Mutex
	changed chan struct{}
}

func (q *LoginQueue) notifyLogin() {
	q.mu.Lock()
	close(q.changed)
	q.changed = make(chan struct{})
	q.mu.Unlock()
}

// acquire returns a logged-in user with its mutex held. Caller must unlock it
// after the complete database operation, including error handling.
func (q *LoginQueue) acquire(ctx context.Context) (*SimulatedUser, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.mu.Lock()
		changed := q.changed
		q.mu.Unlock()
		for _, i := range rand.Perm(len(q.users)) {
			u := q.users[i]
			u.mu.Lock()
			if ctx.Err() != nil {
				u.mu.Unlock()
				return nil, ctx.Err()
			}
			if u.online {
				return u, nil
			}
			u.mu.Unlock()
		}
		// The notification is captured before scanning to avoid missing a login.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func randomSessionDelay() time.Duration {
	return time.Duration(1+rand.Intn(10)) * time.Minute
}

func waitFor(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (q *LoginQueue) runUser(ctx context.Context, u *SimulatedUser) {
	defer func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.online = false
		if err := logout(u.TenantID); err != nil {
			log.Printf("Shutdown logout %s: %v", u.Username, err)
		}
	}()
	for {
		delay := randomSessionDelay()
		log.Printf("%s: logged in; logout in %s", u.Username, delay)
		if !waitFor(ctx, delay) {
			return
		}

		u.mu.Lock()
		// Stop accepting writes even if deleting the session fails. Retry logout
		// before allowing another login, so old sessions do not accumulate.
		u.online = false
		err := logout(u.TenantID)
		u.mu.Unlock()
		for err != nil {
			log.Printf("Logout %s failed: %v; retry in 10s", u.Username, err)
			if !waitFor(ctx, 10*time.Second) {
				return
			}
			u.mu.Lock()
			err = logout(u.TenantID)
			u.mu.Unlock()
		}

		delay = randomSessionDelay()
		log.Printf("%s: logged out; login in %s", u.Username, delay)
		if !waitFor(ctx, delay) {
			return
		}
		for {
			if ctx.Err() != nil {
				return
			}
			u.mu.Lock()
			_, err = login(u.TenantID, u.Username)
			if err == nil {
				u.online = true
			}
			u.mu.Unlock()
			if err == nil {
				q.notifyLogin()
				break
			}
			log.Printf("Login %s failed: %v; retry in 10s", u.Username, err)
			if !waitFor(ctx, 10*time.Second) {
				return
			}
		}
	}
}

type CreateJob struct {
	TenantID string
}

type TestAssociation struct {
	ID   string
	Name string
}

func AssociationID(name string) string {
	var id strings.Builder

	for _, word := range strings.Fields(name) {
		letters := []rune(word)
		id.WriteRune(unicode.ToUpper(letters[0]))
	}

	return id.String()
}

func buildAssociations() []TestAssociation {
	locations := []string{
		"Atlanta", "Boston", "Chicago", "Denver", "Eugene",
		"Fresno", "Greenville", "Houston", "Indianapolis", "Jacksonville",
	}

	sports := []string{
		"Basketball", "Football", "Golf", "Hockey", "Lacrosse",
		"Rowing", "Soccer", "Tennis", "Volleyball", "Wrestling",
	}

	list := make([]TestAssociation, 0, 100)

	for _, location := range locations {
		for _, sport := range sports {
			name := location + " " + sport + " Association"

			list = append(list, TestAssociation{
				ID:   AssociationID(name),
				Name: name,
			})
		}
	}

	return list
}

var associations = buildAssociations()

func connectMongoDB() (*mongo.Client, *mongo.Database, error) {

	fmt.Printf("Retrieving MongoDB connection details from environment variables...\n")

	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		log.Fatal("MONGODB_URI is required")
	}

	dbName := os.Getenv("MONGODB_NAME")
	if dbName == "" {
		log.Fatal("MONGODB_NAME is required")
	}

	fmt.Println("Connecting to MongoDB")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, nil, err
	}

	fmt.Println("Connected to MongoDB")

	// Connect can return before a connection is established.
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, nil, err
	}

	db := client.Database(dbName)

	log.Printf("Connected to database %s",
		db.Name())

	return client, db, nil
}

func RandomHighSchool() string {
	return highSchools[rand.Intn(len(highSchools))]
}

func RandomSport() string {
	return sports[rand.Intn(len(sports))]
}

func RandomLevel() string {
	return levels[rand.Intn(len(levels))]
}

func RandomFirstName() string {
	return firstNames[rand.Intn(len(firstNames))]
}

func RandomLastName() string {
	return lastNames[rand.Intn(len(lastNames))]
}

func RandomPassword(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := 0; i < n; i++ {
		result[i] = letters[rand.Intn(len(letters))]
	}
	return string(result)
}

func RandomEmail() string {
	first := firstNames[rand.Intn(len(firstNames))]
	last := lastNames[rand.Intn(len(lastNames))]

	return strings.ToLower(first + "." + last + "@test.com")
}

func ExtractUsernameFromEmail(email string) string {
	username := strings.SplitN(email, "@", 2)[0]
	names := strings.Split(username, ".")

	for i, name := range names {
		letters := []rune(strings.ToLower(name))
		if len(letters) > 0 {
			letters[0] = unicode.ToUpper(letters[0])
		}
		names[i] = string(letters)
	}

	return strings.Join(names, " ")
}

func RandomLocation() string {
	return locations[rand.Intn(len(locations))]
}

func CreateRandomUser() model.UserRequest {

	userName := RandomEmail()

	return model.UserRequest{
		Username: userName,
		Password: RandomPassword(12),
		Name:     ExtractUsernameFromEmail(userName),
	}
}

func RandomTenantID(tenantIds []string) string {
	return tenantIds[rand.Intn(len(tenantIds))]
}

func RandomAssociation() TestAssociation {
	return associations[rand.Intn(len(associations))]
}

func cleanupDatabase() {

	collections := []string{
		"associations",
		"sites",
		"officials",
		"levels",
		"users",
		"sports",
		"games",
		"sessions",
	}

	for _, name := range collections {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		result, err := db.Collection(name).DeleteMany(ctx, bson.M{})
		cancel()

		if err != nil {
			log.Printf("Failed to clear %s: %v", name, err)
			return
		}

		fmt.Printf("Deleted %d documents from %s\n", result.DeletedCount, name)
	}
}

func logout(tenantId string) error {
	if tenantId == "" {
		return fmt.Errorf("tenantId is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := database.Client.
		Database(database.GetMongoDbName()).
		Collection("sessions").
		DeleteMany(ctx, bson.M{"tenantId": tenantId})

	if err != nil {
		return fmt.Errorf("failed to delete sessions for account %s: %w", tenantId, err)
	}

	return nil
}

func login(tenantId string, username string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID := uuid.New().String()

	session := model.Session{
		SessionID: sessionID,
		Username:  username,
		TenantID:  tenantId,
		ExpiresAt: time.Now().Add(60 * time.Minute),
		Role:      "user",
	}

	_, err := database.Client.
		Database(database.GetMongoDbName()).
		Collection("sessions").
		InsertOne(ctx, session)

	if err != nil {
		return "", err
	}
	return sessionID, nil
}

func main() {
	utils.AuditLog = log.New(os.Stderr, "AUDIT: ", log.LstdFlags)

	numUsers := flag.Int("users", 100, "Number of users to create")
	numJobs := flag.Int("jobs", 10, "Number of jobs to send; -1 runs indefinitely")
	jobInterval := flag.Duration("job-interval", time.Second, "Delay between jobs (for example 1s or 250ms)")

	flag.Parse()

	if *numUsers < 1 {
		log.Fatal("-users must be greater than zero")
	}

	if *numJobs < -1 {
		log.Fatal("-jobs must be zero or greater, or -1 for unlimited")
	}

	if *jobInterval < 0 {
		log.Fatal("-job-interval cannot be negative")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	queue := &LoginQueue{changed: make(chan struct{})}
	var sessionWG sync.WaitGroup
	tenantIds := make([]string, 0, *numUsers)

	associationJobs := make(chan CreateJob)
	siteJobs := make(chan CreateJob)
	officialsJobs := make(chan CreateJob)
	levelsJobs := make(chan CreateJob)
	gamesJobs := make(chan CreateJob)

	var wg sync.WaitGroup
	var err error

	client, db, err = connectMongoDB()
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}

	// Keep the connection open until main finishes.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := client.Disconnect(ctx); err != nil {
			log.Printf("Disconnect from MongoDB: %v", err)
		}
	}()

	// Set the package globals used by your database wrappers.
	database.Client = client
	database.Database = db.Name()

	cleanupDatabase()

	if err := uc.Init(client); err != nil {
		log.Printf("Failed to initialize users collection: %v", err)
		return
	}

	if err := ac.Init(client); err != nil {
		log.Printf("Failed to initialize associations collection: %v", err)
		return
	}

	if err := sc.Init(client); err != nil {
		log.Printf("Failed to initialize sites collection: %v", err)
		return
	}

	if err := oc.Init(client); err != nil {
		log.Printf("Failed to initialize officials collection: %v", err)
		return
	}

	if err := lc.Init(client); err != nil {
		log.Printf("Failed to initialize levels collection: %v", err)
		return
	}

	if err := sp.Init(client); err != nil {
		log.Printf("Failed to initialize sports collection: %v", err)
		return
	}

	for i := 0; i < len(sports); i++ {
		sport := database.Sport{
			ID:     sports[i],
			Name:   sports[i],
			Active: true,
		}

		if err := testcatalog.SeedSport(ctx, db, sport); err != nil {
			fmt.Printf("Failed to add sport %s: %v", sport.Name, err)
			continue
		}
		fmt.Printf("Created sport: %s\n", sport.Name)
	}

	for i := 0; i < *numUsers; i++ {
		user := CreateRandomUser()

		// Give each operation its own timeout.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		tenantID, err := uc.Add(ctx, user)
		cancel()

		if err != nil {
			switch {
			case errors.Is(err, database.ErrUserAlreadyExists):
				fmt.Printf("User already exists: %s\n", user.Username)

			case errors.Is(err, database.ErrInvalidEmail):
				fmt.Printf("Invalid email address: %s\n", user.Username)

			default:
				fmt.Printf("Could not create user account: %v\n", err)
			}
			continue
		}

		_, err = login(tenantID, user.Username)
		if err != nil {
			fmt.Printf("Failed to login user %s: %v\n", user.Username, err)
			continue
		}

		tenantIds = append(tenantIds, tenantID)
		queue.users = append(queue.users, &SimulatedUser{TenantID: tenantID, Username: user.Username, online: true})
		fmt.Printf("Created user: %s\n", user.Username)
	}

	fmt.Printf("Tenant Ids: %v\n", tenantIds)

	if len(tenantIds) == 0 {
		wg.Wait()
		log.Print("No users were created; no jobs to send")
		return
	}

	for _, user := range queue.users {
		sessionWG.Add(1)
		go func(u *SimulatedUser) { defer sessionWG.Done(); queue.runUser(runCtx, u) }(user)
	}

	//
	// Start a worker goroutine to process association jobs.
	//

	wg.Add(1)

	go func() {
		defer wg.Done()

		associationsAdded := 0
		associationErrors := 0

		for job := range associationJobs {
			func() {
				user, err := queue.acquire(runCtx)
				if err != nil {
					return
				}
				defer user.mu.Unlock()
				job.TenantID = user.TenantID

				assoc := RandomAssociation()
				email := RandomEmail()
				contact := ExtractUsernameFromEmail(email)
				association := database.Association{
					Id:        AssociationID(assoc.Name),
					Name:      assoc.Name,
					Contact:   contact,
					Phone:     "",
					Email:     email,
					Assignors: contact,
				}
				fmt.Println("Association Worker received job for tenant:", job.TenantID)
				if err := ac.Add(association, job.TenantID); err != nil {
					fmt.Printf(
						"Failed to create association for tenant %s: %v\n",
						job.TenantID,
						err,
					)
					associationErrors++
					return
				}

				associationsAdded++
				fmt.Printf(
					"Created association %s for tenant %s\n",
					association.Id,
					job.TenantID,
				)
			}()
		}

		fmt.Printf("Association Worker: no more jobs (Added: %d, Errors: %d)\n", associationsAdded, associationErrors)
	}()

	//
	// Start a worker goroutine to process site jobs.
	//
	wg.Add(1)

	go func() {
		defer wg.Done()
		sitesAdded := 0
		siteErrors := 0

		for job := range siteJobs {
			func() {
				user, err := queue.acquire(runCtx)
				if err != nil {
					return
				}
				defer user.mu.Unlock()
				job.TenantID = user.TenantID

				email := RandomEmail()
				contact := ExtractUsernameFromEmail(email)

				site := database.Site{
					Name:    RandomLocation(),
					Contact: contact,
					Phone:   "",
					Email:   email,
				}
				fmt.Println(
					"Site Worker received job for tenant:",
					job.TenantID,
				)
				if err := sc.Add(site, job.TenantID); err != nil {
					fmt.Printf(
						"Failed to create site for tenant %s: %v",
						job.TenantID,
						err,
					)
					siteErrors++
					return
				}
				fmt.Printf(
					"Created site %s for tenant %s\n",
					site.Name,
					job.TenantID,
				)
				sitesAdded++
			}()
		}

		fmt.Printf("Site Worker: no more jobs (Added: %d, Errors: %d)\n", sitesAdded, siteErrors)
	}()

	//
	// Start a worker goroutine to process officials jobs.
	//

	wg.Add(1)

	go func() {
		defer wg.Done()
		officialsAdded := 0
		officialErrors := 0

		for job := range officialsJobs {
			func() {
				user, err := queue.acquire(runCtx)
				if err != nil {
					return
				}
				defer user.mu.Unlock()
				job.TenantID = user.TenantID

				official := database.Official{
					FirstName: RandomFirstName(),
					LastName:  RandomLastName(),
					Phone:     "",
					Email:     RandomEmail(),
					Address:   "",
				}

				fmt.Println(
					"Official Worker received job for tenant:",
					job.TenantID,
				)

				if err := oc.Add(official, job.TenantID); err != nil {
					fmt.Printf(
						"Failed to create official for tenant %s: %v",
						job.TenantID,
						err,
					)
					officialErrors++
					return
				}

				officialsAdded++
				fmt.Printf(
					"Created official %s %s for tenant %s\n",
					official.FirstName,
					official.LastName,
					job.TenantID,
				)
			}()
		}

		fmt.Printf("Official Worker: no more jobs (Added: %d, Errors: %d)\n", officialsAdded, officialErrors)
	}()

	//
	// Start a worker goroutine to process levels jobs.
	//

	wg.Add(1)

	go func() {
		defer wg.Done()
		levelsAdded := 0
		levelErrors := 0

		for job := range levelsJobs {
			func() {
				user, err := queue.acquire(runCtx)
				if err != nil {
					return
				}
				defer user.mu.Unlock()
				job.TenantID = user.TenantID

				level := database.Level{
					Sport:    RandomSport(),
					GameFee:  0,
					Name:     RandomLevel(),
					Active:   true,
					TenantID: job.TenantID,
				}

				fmt.Println(
					"Level Worker received job for tenant:",
					job.TenantID,
				)

				if err := lc.Add(level, job.TenantID); err != nil {
					fmt.Printf(
						"Failed to create level for tenant %s: %v",
						job.TenantID,
						err,
					)
					levelErrors++
					return
				}

				levelsAdded++
				fmt.Printf(
					"Created level %s for tenant %s\n",
					level.Name,
					job.TenantID,
				)
			}()
		}

		fmt.Printf("Level Worker: no more jobs (Added: %d, Errors: %d)\n", levelsAdded, levelErrors)
	}()

	//
	// Start a worker goroutine to process games jobs.
	//

	wg.Add(1)

	go func() {
		defer wg.Done()
		gamesAdded := 0
		gameErrors := 0

		for job := range gamesJobs {
			func() {
				user, err := queue.acquire(runCtx)
				if err != nil {
					return
				}
				defer user.mu.Unlock()
				job.TenantID = user.TenantID

				fmt.Println(
					"Game Worker received job for tenant:",
					job.TenantID,
				)

				gamesAdded++
			}()
		}

		fmt.Printf("Game Worker: no more jobs (Added: %d, Errors: %d)\n", gamesAdded, gameErrors)
	}()

dispatch:
	for i := 0; *numJobs == -1 || i < *numJobs; i++ {
		// Workers choose an online account when executing, avoiding stale account
		// selections while a job is waiting on a channel.
		channels := []chan CreateJob{associationJobs, siteJobs, officialsJobs, levelsJobs}
		jobs := channels[rand.Intn(len(channels))]
		select {
		case <-runCtx.Done():
			break dispatch
		case jobs <- CreateJob{}:
		}
		if !waitFor(runCtx, *jobInterval) {
			break dispatch
		}
	}

	close(associationJobs)
	close(siteJobs)
	close(officialsJobs)
	close(levelsJobs)
	close(gamesJobs)

	wg.Wait()
	cancelRun()
	sessionWG.Wait()
	fmt.Println("All workers finished and simulated sessions logged out; main can exit")

}
