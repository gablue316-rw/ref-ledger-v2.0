package database

import (
	"crypto/sha256"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
)

func (v *AssociationDoc) UnmarshalBSON(data []byte) error {
	type plain AssociationDoc
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = AssociationDoc(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}

func (v *SiteDoc) UnmarshalBSON(data []byte) error {
	type plain SiteDoc
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = SiteDoc(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}

func (v *OfficialDoc) UnmarshalBSON(data []byte) error {
	type plain OfficialDoc
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = OfficialDoc(decoded)
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

func (v *Level) UnmarshalBSON(data []byte) error {
	type plain Level
	var decoded plain
	if err := bson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = Level(decoded)
	v.Version = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}
