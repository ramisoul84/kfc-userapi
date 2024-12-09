package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
)

// Sentinel errors so callers can distinguish causes.
var (
	ErrTokenExpired = errors.New("token expired")
	ErrTokenInvalid = errors.New("token invalid")
)

// DeviceTokenManager issues and verifies device JWTs.
type DeviceTokenManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewDeviceTokenManager(secret, issuer string, ttl time.Duration) *DeviceTokenManager {
	return &DeviceTokenManager{
		secret: []byte(secret),
		issuer: issuer,
		ttl:    ttl,
	}
}

// TTL returns the configured token lifetime.
func (m *DeviceTokenManager) TTL() time.Duration { return m.ttl }

// Issue creates a signed JWT for a device.
func (m *DeviceTokenManager) Issue(
	deviceID uuid.UUID,
	serial string,
	restaurantID uuid.UUID,
) (*domain.DeviceTokenClaims, string, error) {
	now := time.Now().UTC()
	claims := &domain.DeviceTokenClaims{
		DeviceID:     deviceID,
		SerialNumber: serial,
		RestaurantID: restaurantID,
		TokenID:      uuid.NewString(),
		IssuedAt:     now,
		NotBefore:    now,
		ExpiresAt:    now.Add(m.ttl),
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":           m.issuer,
		"sub":           claims.DeviceID.String(),
		"jti":           claims.TokenID,
		"device_id":     claims.DeviceID.String(),
		"serial_number": claims.SerialNumber,
		"restaurant_id": claims.RestaurantID.String(),
		"iat":           claims.IssuedAt.Unix(),
		"nbf":           claims.NotBefore.Unix(),
		"exp":           claims.ExpiresAt.Unix(),
	})

	signed, err := tok.SignedString(m.secret)
	if err != nil {
		return nil, "", fmt.Errorf("sign device token: %w", err)
	}
	return claims, signed, nil
}

// Verify parses and validates a raw JWT, returning its claims.
func (m *DeviceTokenManager) Verify(raw string) (*domain.DeviceTokenClaims, error) {
	parsed, err := jwt.Parse(raw,
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithIssuer(m.issuer),
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	if !parsed.Valid {
		return nil, ErrTokenInvalid
	}

	mapClaims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected claims type", ErrTokenInvalid)
	}

	deviceID, err := uuid.Parse(stringClaim(mapClaims, "device_id"))
	if err != nil {
		return nil, fmt.Errorf("%w: bad device_id", ErrTokenInvalid)
	}
	restaurantID, err := uuid.Parse(stringClaim(mapClaims, "restaurant_id"))
	if err != nil {
		return nil, fmt.Errorf("%w: bad restaurant_id", ErrTokenInvalid)
	}

	exp, _ := mapClaims.GetExpirationTime()
	iat, _ := mapClaims.GetIssuedAt()
	nbf, _ := mapClaims.GetNotBefore()

	return &domain.DeviceTokenClaims{
		DeviceID:     deviceID,
		SerialNumber: stringClaim(mapClaims, "serial_number"),
		RestaurantID: restaurantID,
		TokenID:      stringClaim(mapClaims, "jti"),
		IssuedAt:     iat.Time,
		NotBefore:    nbf.Time,
		ExpiresAt:    exp.Time,
	}, nil
}

func stringClaim(c jwt.MapClaims, key string) string {
	if v, ok := c[key].(string); ok {
		return v
	}
	return ""
}
