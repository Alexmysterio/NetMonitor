package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserKey contextKey = "user"

type User struct {
	ID                string   `json:"id"`
	Username          string   `json:"username"`
	DisplayName       string   `json:"display_name"`
	Role              string   `json:"role"`
	RestrictMaps      bool     `json:"restrict_maps"`
	RestrictGroups    bool     `json:"restrict_groups"`
	RestrictDevices   bool     `json:"restrict_devices"`
	AllowedProbeTypes []string `json:"allowed_probe_types"`
}

type Claims struct {
	UserID   string `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Version  int    `json:"ver"`
	jwt.RegisteredClaims
}

func Issue(secret, userID, username, role string, tokenVersion int) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		Version:  tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			Issuer:    "netmonitor",
			Subject:   userID,
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func Parse(secret, raw string) (*Claims, error) {
	t, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	}, jwt.WithIssuer("netmonitor"))
	if err != nil {
		return nil, err
	}
	claims, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, UserKey, u)
}

func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(UserKey).(User)
	return u, ok
}
