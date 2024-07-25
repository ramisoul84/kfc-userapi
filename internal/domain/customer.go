package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// CustomerClaims identify a signed-in website customer (a loyalty member).
type CustomerClaims struct {
	MemberID  uuid.UUID
	TokenID   string
	ExpiresAt time.Time
}

// KioskLoyaltyClaims let one kiosk act for a customer who identified
// themselves with a member code, for a few minutes.
type KioskLoyaltyClaims struct {
	MemberID  uuid.UUID
	DeviceID  uuid.UUID
	ExpiresAt time.Time
}

// NormalizePhone turns user input into E.164. Russian habits are
// accepted: "8 999 …" and "9 99 …" (10 digits) both become "+7999…".
// Returns "" if the input can't be a phone number.
func NormalizePhone(raw string) string {
	plus := strings.HasPrefix(strings.TrimSpace(raw), "+")
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()

	switch {
	case !plus && len(d) == 11 && d[0] == '8':
		d = "7" + d[1:]
	case !plus && len(d) == 10 && d[0] == '9':
		d = "7" + d
	}
	if len(d) < 10 || len(d) > 15 || d[0] == '0' {
		return ""
	}
	return "+" + d
}

// MaskPhone hides the middle of a phone number for logs: +7999***4567.
func MaskPhone(p string) string {
	if len(p) < 8 {
		return "***"
	}
	return p[:5] + "***" + p[len(p)-4:]
}
