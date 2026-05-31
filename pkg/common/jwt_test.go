package common

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	// Token should have 3 parts (header.payload.signature)
	parts := strings.Split(token, ".")
	assert.Equal(t, 3, len(parts))
}

func TestValidateToken_Valid(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)

	claims, err := ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, "player123", claims.PlayerID)
	assert.Equal(t, "testuser", claims.Username)
	assert.Equal(t, "astra-game", claims.Issuer)
}

func TestValidateToken_Expired(t *testing.T) {
	// Create an expired token manually
	originalSecret := JWTSecret
	JWTSecret = []byte("test-secret-for-expiry")
	defer func() { JWTSecret = originalSecret }()

	claims := &JWTClaims{
		PlayerID: "player123",
		Username: "testuser",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)), // expired
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			Issuer:    "astra-game",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(JWTSecret)
	assert.NoError(t, err)

	_, err = ValidateToken(tokenString)
	assert.Error(t, err)
}

func TestValidateToken_Tampered(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)

	// Tamper with the token by changing a character
	tampered := token[:len(token)-5] + "XXXXX"
	_, err = ValidateToken(tampered)
	assert.Error(t, err)
}

func TestValidateToken_Empty(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"empty string", ""},
		{"whitespace", "   "},
		{"random string", "not-a-jwt-token"},
		{"partial jwt", "header.payload"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateToken(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)

	// Temporarily change secret so validation fails
	originalSecret := JWTSecret
	JWTSecret = []byte("wrong-secret")
	defer func() { JWTSecret = originalSecret }()

	_, err = ValidateToken(token)
	assert.Error(t, err)
}

func TestGetPlayerIDFromToken(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)

	playerID, err := GetPlayerIDFromToken(token)
	assert.NoError(t, err)
	assert.Equal(t, "player123", playerID)
}

func TestGetUsernameFromToken(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)

	username, err := GetUsernameFromToken(token)
	assert.NoError(t, err)
	assert.Equal(t, "testuser", username)
}

func TestIsTokenExpired(t *testing.T) {
	token, err := GenerateToken("player123", "testuser")
	assert.NoError(t, err)

	expired, err := IsTokenExpired(token)
	assert.NoError(t, err)
	assert.False(t, expired)
}
