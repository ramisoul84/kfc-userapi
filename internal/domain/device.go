package domain

import (
	"time"

	"github.com/google/uuid"
)

// Device holds the minimum metadata needed to issue a device token.
// This mirrors what CRM stores in Postgres; userapi only needs the
// subset that goes into the token claims.
type Device struct {
	ID           uuid.UUID
	SerialNumber string
	RestaurantID uuid.UUID
	IsActive     bool
}

// DeviceTokenClaims are the claims embedded in a device JWT.
type DeviceTokenClaims struct {
	DeviceID     uuid.UUID
	SerialNumber string
	RestaurantID uuid.UUID
	TokenID      string // jti — unique per issued token, for revocation
	IssuedAt     time.Time
	NotBefore    time.Time
	ExpiresAt    time.Time
}
