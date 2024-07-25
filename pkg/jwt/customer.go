package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
)

// Token audiences keep a customer token from being replayed as a kiosk
// session token and vice versa.
const (
	audCustomer     = "kfc-customer"
	audKioskLoyalty = "kfc-kiosk-loyalty"
)

// CustomerTokenManager issues the short-lived customer access token and
// the kiosk loyalty session token. Separate secret from device tokens.
type CustomerTokenManager struct {
	secret    []byte
	issuer    string
	accessTTL time.Duration
	kioskTTL  time.Duration
}

func NewCustomerTokenManager(secret, issuer string, accessTTL, kioskTTL time.Duration) *CustomerTokenManager {
	return &CustomerTokenManager{secret: []byte(secret), issuer: issuer, accessTTL: accessTTL, kioskTTL: kioskTTL}
}

func (m *CustomerTokenManager) IssueAccess(memberID uuid.UUID) (string, time.Time, error) {
	exp := time.Now().Add(m.accessTTL)
	tok, err := m.sign(jwt.MapClaims{
		"iss": m.issuer,
		"aud": audCustomer,
		"sub": memberID.String(),
		"jti": uuid.NewString(),
		"iat": time.Now().Unix(),
		"exp": exp.Unix(),
	})
	return tok, exp, err
}

func (m *CustomerTokenManager) VerifyAccess(raw string) (*domain.CustomerClaims, error) {
	c, err := m.parse(raw, audCustomer)
	if err != nil {
		return nil, err
	}
	memberID, err := uuid.Parse(stringClaim(c, "sub"))
	if err != nil {
		return nil, fmt.Errorf("%w: bad sub", ErrTokenInvalid)
	}
	exp, _ := c.GetExpirationTime()
	return &domain.CustomerClaims{MemberID: memberID, TokenID: stringClaim(c, "jti"), ExpiresAt: exp.Time}, nil
}

// IssueKioskSession binds a member to one kiosk for a few minutes.
func (m *CustomerTokenManager) IssueKioskSession(memberID, deviceID uuid.UUID) (string, time.Time, error) {
	exp := time.Now().Add(m.kioskTTL)
	tok, err := m.sign(jwt.MapClaims{
		"iss":       m.issuer,
		"aud":       audKioskLoyalty,
		"sub":       memberID.String(),
		"device_id": deviceID.String(),
		"iat":       time.Now().Unix(),
		"exp":       exp.Unix(),
	})
	return tok, exp, err
}

func (m *CustomerTokenManager) VerifyKioskSession(raw string) (*domain.KioskLoyaltyClaims, error) {
	c, err := m.parse(raw, audKioskLoyalty)
	if err != nil {
		return nil, err
	}
	memberID, err1 := uuid.Parse(stringClaim(c, "sub"))
	deviceID, err2 := uuid.Parse(stringClaim(c, "device_id"))
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("%w: bad claims", ErrTokenInvalid)
	}
	exp, _ := c.GetExpirationTime()
	return &domain.KioskLoyaltyClaims{MemberID: memberID, DeviceID: deviceID, ExpiresAt: exp.Time}, nil
}

func (m *CustomerTokenManager) sign(claims jwt.MapClaims) (string, error) {
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign customer token: %w", err)
	}
	return s, nil
}

func (m *CustomerTokenManager) parse(raw, audience string) (jwt.MapClaims, error) {
	parsed, err := jwt.Parse(raw,
		func(t *jwt.Token) (interface{}, error) { return m.secret, nil },
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(audience),
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	c, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, ErrTokenInvalid
	}
	return c, nil
}
