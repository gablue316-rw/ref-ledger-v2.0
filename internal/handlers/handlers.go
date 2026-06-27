package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"ref-ledger-v2/internal/database"
	"ref-ledger-v2/internal/email"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type PasswordReset struct {
	Token     string    `bson:"token"`
	Email     string    `bson:"email"`
	ExpiresAt time.Time `bson:"expiresAt"`
	Used      bool      `bson:"used"`
	CreatedAt time.Time `bson:"createdAt"`
}

func generateResetToken() (string, error) {
	bytes := make([]byte, 32)

	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}

func ForgotPasswordHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Println("ForgotPasswordHandler...return[1]")
		return
	}

	var req ForgotPasswordRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Always return same response so nobody can discover valid emails.
	genericResponse := func() {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(bson.M{
			"message": "If an account exists, a password reset email has been sent.",
		})
	}

	if req.Email == "" {
		genericResponse()
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	db := database.Client.Database("refLedger_v2")
	users := db.Collection("users")
	resets := db.Collection("passwordResets")

	var user bson.M

	err = users.FindOne(ctx, bson.M{
		"username": req.Email,
	}).Decode(&user)

	if err == mongo.ErrNoDocuments {
		genericResponse()
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	token, err := generateResetToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	reset := PasswordReset{
		Token:     token,
		Email:     req.Email,
		ExpiresAt: time.Now().Add(15 * time.Minute),
		Used:      false,
		CreatedAt: time.Now(),
	}

	_, err = resets.InsertOne(ctx, reset)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = sendPasswordResetEmail(req.Email, token)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	genericResponse()
}

func ResetPasswordHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req ResetPasswordRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if req.Token == "" || req.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(bson.M{
			"message": "Token and password are required.",
		})
		return
	}

	if len(req.Password) < 8 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(bson.M{
			"message": "Password must be at least 8 characters.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	db := database.Client.Database("refLedger_v2")
	users := db.Collection("users")
	resets := db.Collection("passwordResets")

	var reset PasswordReset

	err = resets.FindOne(ctx, bson.M{
		"token": req.Token,
		"used":  false,
		"expiresAt": bson.M{
			"$gt": time.Now(),
		},
	}).Decode(&reset)

	if err == mongo.ErrNoDocuments {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(bson.M{
			"message": "Invalid or expired reset token.",
		})
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password),
		bcrypt.DefaultCost,
	)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	_, err = users.UpdateOne(ctx,
		bson.M{
			"username": reset.Email,
		},
		bson.M{
			"$set": bson.M{
				"passwordHash": string(hashedPassword),
			},
		},
	)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	_, _ = resets.UpdateOne(ctx,
		bson.M{
			"token": req.Token,
		},
		bson.M{
			"$set": bson.M{
				"used": true,
			},
		},
	)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(bson.M{
		"message": "Password reset successfully.",
	})
}

func sendPasswordResetEmail(to string, token string) error {

	resetURL := fmt.Sprintf(
		"https://ref-ledger.com/resetPassword?token=%s",
		token,
	)

	var email email.Email
	email.Initialize()

	body := fmt.Sprintf(`
You requested a password reset for Ref-Ledger.

Click the link below to reset your password:

%s

This link expires in 15 minutes.

If you did not request this reset, you can ignore this email.
`, resetURL)

	// Send report via email
	email.SetSubject("Ref-Ledger Password Reset")
	email.SetBody(body)
	email.SetTo([]string{to})
	email.Send()

	return nil
}
