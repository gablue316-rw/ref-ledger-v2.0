package model

var ModelsVersion string = "ref-ledger-models-v2.1.0"

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

type GameFilter struct {
	Status      []string `json:"status,omitempty"`
	Association []string `json:"association,omitempty"`
	GameId      []int64  `json:"gameId,omitempty"`
}
