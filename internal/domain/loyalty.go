package domain

import (
	"strings"
	"time"

	loyaltyv1 "github.com/ramisoul84/kfc-userapi/gen/loyalty/v1"
)

// QRPrefix marks a KFC member code inside a QR payload, so a kiosk
// scanner can tell it apart from any other QR code.
const QRPrefix = "KFC-LOYALTY:"

// Member is a loyalty member as returned to the website.
type Member struct {
	ID              string    `json:"id"`
	Phone           string    `json:"phone,omitempty"`
	FirstName       string    `json:"first_name,omitempty"`
	Email           string    `json:"email,omitempty"`
	PointsBalance   int       `json:"points_balance"`
	PointsAvailable int       `json:"points_available"`
	LifetimePoints  int       `json:"lifetime_points,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitzero"`
}

// ForKiosk keeps only what a kiosk screen should show.
func (m *Member) ForKiosk() *Member {
	return &Member{
		ID:              m.ID,
		FirstName:       m.FirstName,
		PointsBalance:   m.PointsBalance,
		PointsAvailable: m.PointsAvailable,
	}
}

func MemberFromProto(m *loyaltyv1.Member) *Member {
	return &Member{
		ID:              m.GetId(),
		Phone:           m.GetPhone(),
		FirstName:       m.GetFirstName(),
		Email:           m.GetEmail(),
		PointsBalance:   int(m.GetPointsBalance()),
		PointsAvailable: int(m.GetPointsAvailable()),
		LifetimePoints:  int(m.GetLifetimePoints()),
		CreatedAt:       time.UnixMilli(m.GetCreatedAt()).UTC(),
	}
}

// Program is the set of loyalty rules shown to customers.
type Program struct {
	EarnPercent      int     `json:"earn_percent"`
	MaxRedeemPercent int     `json:"max_redeem_percent"`
	WelcomeBonus     int     `json:"welcome_bonus"`
	PointValue       float64 `json:"point_value"` // rubles per point
}

func ProgramFromProto(p *loyaltyv1.Program) *Program {
	return &Program{
		EarnPercent:      int(p.GetEarnPercent()),
		MaxRedeemPercent: int(p.GetMaxRedeemPercent()),
		WelcomeBonus:     int(p.GetWelcomeBonus()),
		PointValue:       Rubles(p.GetPointValueKopecks()),
	}
}

type PointsTransaction struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Points       int       `json:"points"`
	BalanceAfter int       `json:"balance_after"`
	OrderID      string    `json:"order_id,omitempty"`
	OrderNumber  int       `json:"order_number,omitempty"`
	Description  string    `json:"description,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type PointsHistory struct {
	Transactions []PointsTransaction `json:"transactions"`
	Total        int                 `json:"total"`
}

func PointsHistoryFromProto(r *loyaltyv1.ListTransactionsResponse) *PointsHistory {
	out := &PointsHistory{Transactions: []PointsTransaction{}, Total: int(r.GetTotal())}
	for _, t := range r.GetTransactions() {
		out.Transactions = append(out.Transactions, PointsTransaction{
			ID:           t.GetId(),
			Type:         t.GetType(),
			Points:       int(t.GetPoints()),
			BalanceAfter: int(t.GetBalanceAfter()),
			OrderID:      t.GetOrderId(),
			OrderNumber:  int(t.GetOrderNumber()),
			Description:  t.GetDescription(),
			CreatedAt:    time.UnixMilli(t.GetCreatedAt()).UTC(),
		})
	}
	return out
}

// MemberCode is what the website shows for kiosk identification.
type MemberCode struct {
	Code      string    `json:"code"`
	QRPayload string    `json:"qr_payload"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ParseMemberCode accepts a raw QR payload or typed digits (with spaces)
// and returns just the digits.
func ParseMemberCode(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, QRPrefix)
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
