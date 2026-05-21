package model

var ModelsVersion string = "ref-ledger-models-v2.1.0"

type ExpenseDescriptor struct {
	ExpenseId   string
	Date        string
	Type        string
	Amount      string
	Association string
	GameId      string
	Description string
}

type ExpenseDoc struct {
	ExpenseId   string `bson:"expenseId,omitempty"`
	Date        string `bson:"date,omitempty"`
	Type        string `bson:"type,omitempty"`
	Amount      int64  `bson:"amount,omitempty"`
	Association string `bson:"association,omitempty"`
	GameId      int64  `bson:"gameId,omitempty"`
	Description string `bson:"description,omitempty"`
}

type EFilters struct {
	Association string
	GameId      string
	ExpenseType string
	FromDate    string
	ToDate      string
}

type ExpenseFilter struct {
	Association []string `json:"association,omitempty"`
	GameId      []int64  `json:"gameId,omitempty"`
	ExpenseType []string `json:"type,omitempty"`
	Date        *Date    `json:"date,omitempty"`
}

type PaymentDescriptor struct {
	PaymentId   string
	PaymentDate string
	PaymentAmt  string
	Association string
	GameIds     string
}

type PaymentDoc struct {
	PaymentId   string  `bson:"paymentId,omitempty"`
	PaymentDate string  `bson:"paymentDate,omitempty"`
	PaymentAmt  int64   `bson:"paymentAmt,omitempty"`
	Association string  `bson:"association,omitempty"`
	GameIds     []int64 `bson:"gameIds,omitempty"`
}

type OfficialDescriptor struct {
	FirstName   string
	LastName    string
	Phone       string
	Association string
}

type OfficialDoc struct {
	OfficialId  int    `bson:"officialId,omitempty"`
	FirstName   string `bson:"firstName,omitempty"`
	LastName    string `bson:"lastName,omitempty"`
	Phone       string `bson:"phone,omitempty"`
	Association string `bson:"association,omitempty"`
}

type GameDescriptor struct {
	GameId      string
	Date        string
	Time        string
	Sport       string
	Site        string
	Field       string
	NumOfGames  string
	Level       string
	GameFee     string
	TravelPay   string
	AssignorFee string
	Deductions  string
	Association string
	Status      string
	Referee     string
	U1          string
	U2          string
	ECO         string
	Assignor    string
}

type GameDoc struct {
	GameId      int64  `bson:"gameId,omitempty"`
	Date        string `bson:"date,omitempty"`
	Time        string `bson:"time,omitempty"`
	Sport       string `bson:"sport,omitempty"`
	Site        string `bson:"site,omitempty"`
	Field       string `bson:"field,omitempty"`
	NumOfGames  int64  `bson:"numOfGames,omitempty"`
	Level       string `bson:"level,omitempty"`
	GameFee     int64  `bson:"gameFee,omitempty"`
	TravelPay   int64  `bson:"travelPay,omitempty"`
	AssignorFee int64  `bson:"assignorFee,omitempty"`
	Deductions  int64  `bson:"deductions,omitempty"`
	Association string `bson:"association,omitempty"`
	Status      string `bson:"status,omitempty"`
	Referee     string `bson:"referee,omitempty"`
	U1          string `bson:"u1,omitempty"`
	U2          string `bson:"u2,omitempty"`
	ECO         string `bson:"eco,omitempty"`
	Assignor    string `bson:"assignor,omitempty"`
}

type JsonDoc struct {
	GameId      int64  `json:"gameId"`
	Date        string `json:"date"`
	Time        string `json:"time"`
	Sport       string `json:"sport"`
	Site        string `json:"site"`
	Field       string `json:"field"`
	NumOfGames  int64  `json:"numOfGames"`
	Level       string `json:"level"`
	GameFee     int64  `json:"gameFee"`
	TravelPay   int64  `json:"travelPay"`
	AssignorFee int64  `json:"assignorFee"`
	Deductions  int64  `json:"deductions"`
	Association string `json:"association"`
	Status      string `json:"status"`
	Referee     string `json:"referee"`
	U1          string `json:"u1"`
	U2          string `json:"u2"`
	ECO         string `json:"eco"`
	Assignor    string `json:"assignor"`
}

type Filter struct {
	Field string
	Value []string
}

type Date struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type GFilters struct {
	Status      string
	Association string
	GameId      string
	FromDate    string
	ToDate      string
	Official    string
	Site        string
	Sport       string
	Level       string
	GameFee     string
}

type GameFilter struct {
	Status      []string `json:"status,omitempty"`
	Association []string `json:"association,omitempty"`
	GameId      []int64  `json:"gameId,omitempty"`
	Date        *Date    `json:"date,omitempty"`
	Referee     string   `json:"referee,omitempty"`
	U1          string   `json:"u1,omitempty"`
	U2          string   `json:"u2,omitempty"`
	Site        []string `json:"site,omitempty"`
	Sport       []string `json:"sport,omitempty"`
	Level       []string `json:"level,omitempty"`
}

type Config struct {
	AppName  string         `json:"appName"`
	Version  string         `json:"version"`
	UserName string         `json:"user"`
	Password string         `json:"pwd"`
	Features FeaturesConfig `json:"features"`
}

type FeaturesConfig struct {
	EnableDeleteOnCancel bool   `json:"enableDeleteOnCancel"`
	LogFile              string `json:"logFile"`
	DbUpdateLog          string `json:"dbUpdateLog"`
}

type HtmlResponse struct {
	GameId      int64  `json:"gameId" bson:"gameId,omitempty"`
	Date        string `json:"date" bson:"date,omitempty"`
	Time        string `json:"time" bson:"time,omitempty"`
	Sport       string `json:"sport" bson:"sport,omitempty"`
	Site        string `json:"site" bson:"site,omitempty"`
	Field       string `json:"field" bson:"field,omitempty"`
	NumOfGames  int64  `json:"numOfGames" bson:"numOfGames,omitempty"`
	Level       string `json:"level" bson:"level,omitempty"`
	GameFee     int64  `json:"gameFee" bson:"gameFee,omitempty"`
	TravelPay   int64  `json:"travelPay" bson:"travelPay,omitempty"`
	AssignorFee int64  `json:"assignorFee" bson:"assignorFee,omitempty"`
	Deductions  int64  `json:"deductions" bson:"deductions,omitempty"`
	Association string `json:"association" bson:"association,omitempty"`
	Status      string `json:"status" bson:"status,omitempty"`
	Referee     string `json:"referee" bson:"referee,omitempty"`
	U1          string `json:"u1" bson:"u1,omitempty"`
	U2          string `json:"u2" bson:"u2,omitempty"`
	ECO         string `json:"eco" bson:"eco,omitempty"`
	Assignor    string `json:"assignor" bson:"assignor,omitempty"`
}

type GameView struct {
	GameId      int64  `json:"GameId"`
	Date        string `json:"Date"`
	Time        string `json:"Time"`
	Sport       string `json:"Sport"`
	Site        string `json:"Site"`
	Field       string `json:"Field"`
	NumOfGames  int64  `json:"NumOfGames"`
	Level       string `json:"Level"`
	GameFee     string `json:"GameFee"`
	Association string `json:"Association"`
	Status      string `json:"Status"`
	Officials   string `json:"Officials"`
}
