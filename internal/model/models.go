package model

var ModelsVersion string = "ref-ledger-models-v2.1.0"

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

type Filter struct {
	Field string
	Value string
}
