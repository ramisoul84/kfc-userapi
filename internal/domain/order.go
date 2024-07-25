package domain

import (
	"time"

	orderv1 "github.com/ramisoul84/kfc-userapi/gen/order/v1"
)

// Order statuses (owned by kfc-order).
const (
	OrderStatusAccepted  = "accepted"
	OrderStatusPreparing = "preparing"
	OrderStatusReady     = "ready"
	OrderStatusCompleted = "completed"
	OrderStatusCancelled = "cancelled"
)

// OrderView is the order as screens see it. It is byte-for-byte the
// shape kfc-order publishes on Kafka (domain.OrderView there), so events
// can be relayed to SSE clients without re-encoding. Change both together.
type OrderView struct {
	ID            string     `json:"id"`
	RestaurantID  string     `json:"restaurant_id"`
	Number        int        `json:"number"`
	BusinessDate  string     `json:"business_date"`
	Channel       string     `json:"channel"`
	OrderType     string     `json:"order_type"`
	Status        string     `json:"status"`
	PaymentMethod string     `json:"payment_method"`
	PaymentStatus string     `json:"payment_status"`
	CustomerName  string     `json:"customer_name,omitempty"`
	CustomerPhone string     `json:"customer_phone,omitempty"`
	Note          string     `json:"note,omitempty"`
	DeviceID      string     `json:"device_id,omitempty"`
	Subtotal      float64    `json:"subtotal"`
	Total         float64    `json:"total"`
	Currency      string     `json:"currency"`
	CancelReason  string     `json:"cancel_reason,omitempty"`
	Lines         []LineView `json:"lines"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	PreparingAt   *time.Time `json:"preparing_at,omitempty"`
	ReadyAt       *time.Time `json:"ready_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`

	MemberID       string  `json:"member_id,omitempty"`
	Discount       float64 `json:"discount"`
	PointsRedeemed int     `json:"points_redeemed,omitempty"`
	LoyaltyHoldID  string  `json:"loyalty_hold_id,omitempty"`
}

type LineView struct {
	ID            string  `json:"id"`
	ItemID        string  `json:"item_id"`
	ProductCode   string  `json:"product_code"`
	Name          string  `json:"name"`
	VariationID   string  `json:"variation_id,omitempty"`
	VariationName string  `json:"variation_name,omitempty"`
	Quantity      int     `json:"quantity"`
	UnitPrice     float64 `json:"unit_price"`
	LineTotal     float64 `json:"line_total"`
}

// DisplayOrder is what the hall display gets: just enough to show a
// number in the right column. No names, phones or items on a public screen.
type DisplayOrder struct {
	ID        string     `json:"id"`
	Number    int        `json:"number"`
	Status    string     `json:"status"`
	Channel   string     `json:"channel"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ReadyAt   *time.Time `json:"ready_at,omitempty"`
}

// ToDisplay strips an order down for the hall display.
func (o *OrderView) ToDisplay() *DisplayOrder {
	return &DisplayOrder{
		ID:        o.ID,
		Number:    o.Number,
		Status:    o.Status,
		Channel:   o.Channel,
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
		ReadyAt:   o.ReadyAt,
	}
}

// ToPublic strips fields a customer tracking page doesn't need.
func (o *OrderView) ToPublic() *OrderView {
	c := *o
	c.CustomerPhone = ""
	c.DeviceID = ""
	c.MemberID = ""
	c.LoyaltyHoldID = ""
	return &c
}

// OrderEvent is the Kafka message from kfc-order (orders.events).
type OrderEvent struct {
	Type         string     `json:"type"`
	RestaurantID string     `json:"restaurant_id"`
	Order        *OrderView `json:"order"`
	OccurredAt   time.Time  `json:"occurred_at"`
}

// OrderViewFromProto converts a kfc-order response to the wire view.
func OrderViewFromProto(o *orderv1.Order) *OrderView {
	v := &OrderView{
		ID:            o.GetId(),
		RestaurantID:  o.GetRestaurantId(),
		Number:        int(o.GetNumber()),
		BusinessDate:  o.GetBusinessDate(),
		Channel:       o.GetChannel(),
		OrderType:     o.GetOrderType(),
		Status:        o.GetStatus(),
		PaymentMethod: o.GetPaymentMethod(),
		PaymentStatus: o.GetPaymentStatus(),
		CustomerName:  o.GetCustomerName(),
		CustomerPhone: o.GetCustomerPhone(),
		Note:          o.GetNote(),
		DeviceID:      o.GetDeviceId(),
		Subtotal:      Rubles(o.GetSubtotal()),
		Total:         Rubles(o.GetTotal()),
		Currency:      o.GetCurrency(),
		CancelReason:  o.GetCancelReason(),
		Lines:         make([]LineView, 0, len(o.GetLines())),
		CreatedAt:     time.UnixMilli(o.GetCreatedAt()).UTC(),
		UpdatedAt:     time.UnixMilli(o.GetUpdatedAt()).UTC(),
		PreparingAt:   optionalMillis(o.GetPreparingAt()),
		ReadyAt:       optionalMillis(o.GetReadyAt()),
		CompletedAt:   optionalMillis(o.GetCompletedAt()),
		CancelledAt:   optionalMillis(o.GetCancelledAt()),

		MemberID:       o.GetMemberId(),
		Discount:       Rubles(o.GetDiscount()),
		PointsRedeemed: int(o.GetPointsRedeemed()),
		LoyaltyHoldID:  o.GetLoyaltyHoldId(),
	}
	for _, l := range o.GetLines() {
		v.Lines = append(v.Lines, LineView{
			ID:            l.GetId(),
			ItemID:        l.GetItemId(),
			ProductCode:   l.GetProductCode(),
			Name:          l.GetName(),
			VariationID:   l.GetVariationId(),
			VariationName: l.GetVariationName(),
			Quantity:      int(l.GetQuantity()),
			UnitPrice:     Rubles(l.GetUnitPrice()),
			LineTotal:     Rubles(l.GetLineTotal()),
		})
	}
	return v
}

// Rubles converts kopecks to rubles.
func Rubles(kopecks int64) float64 { return float64(kopecks) / 100 }

func optionalMillis(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}
