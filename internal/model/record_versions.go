package model

import (
	"crypto/sha256"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
)

func (v *GameDoc) UnmarshalBSON(data []byte) error {
	type plain GameDoc
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = GameDoc(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}

func (v *HtmlResponse) UnmarshalBSON(data []byte) error {
	type plain HtmlResponse
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = HtmlResponse(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}

func (v *ExpenseDoc) UnmarshalBSON(data []byte) error {
	type plain ExpenseDoc
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = ExpenseDoc(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}

func (v *PaymentDoc) UnmarshalBSON(data []byte) error {
	type plain PaymentDoc
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = PaymentDoc(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}
