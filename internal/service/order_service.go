package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	menuv1 "github.com/ramisoul84/kfc-userapi/gen/menu/v1"
	orderv1 "github.com/ramisoul84/kfc-userapi/gen/order/v1"
	"github.com/ramisoul84/kfc-userapi/internal/client"
	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// ═══════════════════════════════════════════════════════════════════
// INPUTS
// ═══════════════════════════════════════════════════════════════════

// CartItem is one line as the client sends it: what, not how much.
// Prices are always resolved here from the restaurant's effective menu.
type CartItem struct {
	ItemID      string `json:"item_id"`
	VariationID string `json:"variation_id,omitempty"`
	Quantity    int    `json:"quantity"`
}

// PlaceOrderRequest is the checkout body from the kiosk or the website.
type PlaceOrderRequest struct {
	OrderType      string     `json:"order_type"` // dine_in | takeaway
	CustomerName   string     `json:"customer_name,omitempty"`
	CustomerPhone  string     `json:"customer_phone,omitempty"`
	Note           string     `json:"note,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
	Items          []CartItem `json:"items"`
}

// ═══════════════════════════════════════════════════════════════════
// SERVICE
// ═══════════════════════════════════════════════════════════════════

// OrderService is the customer- and device-facing side of ordering.
type OrderService interface {
	PlaceKioskOrder(ctx context.Context, device *domain.DeviceTokenClaims, req *PlaceOrderRequest) (*domain.OrderView, error)
	PlaceWebOrder(ctx context.Context, restaurantID string, req *PlaceOrderRequest) (*domain.OrderView, error)

	// ListActive returns orders in flight at a restaurant (KDS, display).
	ListActive(ctx context.Context, restaurantID string) ([]*domain.OrderView, error)

	// UpdateFromKitchen changes status on behalf of a KDS. The order must
	// belong to the device's restaurant.
	UpdateFromKitchen(ctx context.Context, device *domain.DeviceTokenClaims, orderID, status string) (*domain.OrderView, error)

	// Get returns one order (customer tracking).
	Get(ctx context.Context, orderID string) (*domain.OrderView, error)
}

type orderService struct {
	orders client.OrderClient
	crm    client.CRMClient
	menu   MenuService
	logger *logger.Logger
}

func NewOrderService(
	orders client.OrderClient,
	crm client.CRMClient,
	menu MenuService,
	log *logger.Logger,
) OrderService {
	return &orderService{orders: orders, crm: crm, menu: menu, logger: log}
}

// Kitchen screens may move orders along these statuses only. Cancelling
// is a manager decision made in CRM, not a tap on the kitchen screen.
var kitchenStatuses = map[string]bool{
	domain.OrderStatusAccepted:  true, // undo "start"
	domain.OrderStatusPreparing: true,
	domain.OrderStatusReady:     true,
	domain.OrderStatusCompleted: true,
}

// Loose international phone check: digits with optional +, spaces,
// dashes and brackets; 10–15 digits overall.
var phonePattern = regexp.MustCompile(`^\+?[0-9 ()\-]{10,20}$`)

// ═══════════════════════════════════════════════════════════════════
// PLACE
// ═══════════════════════════════════════════════════════════════════

func (s *orderService) PlaceKioskOrder(
	ctx context.Context,
	device *domain.DeviceTokenClaims,
	req *PlaceOrderRequest,
) (*domain.OrderView, error) {
	// Kiosks take card payment on the built-in terminal before submitting,
	// so the order reaches the kitchen already paid.
	return s.place(ctx, device.RestaurantID.String(), &orderv1.CreateOrderRequest{
		Channel:       "kiosk",
		PaymentMethod: "card",
		PaymentStatus: "paid",
		DeviceId:      device.DeviceID.String(),
	}, req)
}

func (s *orderService) PlaceWebOrder(
	ctx context.Context,
	restaurantID string,
	req *PlaceOrderRequest,
) (*domain.OrderView, error) {
	name := strings.TrimSpace(req.CustomerName)
	if n := utf8.RuneCountInString(name); n < 2 || n > 60 {
		return nil, domain.NewValidationError("please enter your name (2–60 characters)")
	}
	if !phonePattern.MatchString(strings.TrimSpace(req.CustomerPhone)) {
		return nil, domain.NewValidationError("please enter a valid phone number")
	}

	// The restaurant must exist and be open for orders.
	if _, err := s.crm.GetRestaurant(ctx, restaurantID); err != nil {
		switch status.Code(err) {
		case codes.NotFound, codes.InvalidArgument:
			return nil, domain.NewNotFoundError("restaurant not found")
		default:
			return nil, domain.NewInternalError(err)
		}
	}

	// Online payment is simulated: the website "charges" the card before
	// submitting. A real deployment would create a payment intent here
	// and only submit the order on the provider's success webhook.
	return s.place(ctx, restaurantID, &orderv1.CreateOrderRequest{
		Channel:       "web",
		PaymentMethod: "online",
		PaymentStatus: "paid",
		CustomerName:  name,
		CustomerPhone: strings.TrimSpace(req.CustomerPhone),
	}, req)
}

// place prices the cart against the effective menu and submits it.
func (s *orderService) place(
	ctx context.Context,
	restaurantID string,
	base *orderv1.CreateOrderRequest,
	req *PlaceOrderRequest,
) (*domain.OrderView, error) {
	if req.OrderType != "dine_in" && req.OrderType != "takeaway" {
		return nil, domain.NewValidationError("order_type must be dine_in or takeaway")
	}
	if len(req.Items) == 0 {
		return nil, domain.NewValidationError("your order is empty")
	}
	if utf8.RuneCountInString(req.Note) > 200 {
		return nil, domain.NewValidationError("note is too long")
	}

	menu, err := s.menu.GetMenu(ctx, restaurantID)
	if err != nil {
		if errors.Is(err, ErrMenuNotFound) {
			return nil, domain.NewNotFoundError("restaurant not found")
		}
		return nil, domain.NewInternalError(fmt.Errorf("load menu: %w", err))
	}

	lines, err := priceCart(menu, req.Items)
	if err != nil {
		return nil, err
	}

	base.RestaurantId = restaurantID
	base.OrderType = req.OrderType
	base.Note = strings.TrimSpace(req.Note)
	base.Currency = menu.GetCurrency()
	base.IdempotencyKey = req.IdempotencyKey
	base.Lines = lines

	created, err := s.orders.CreateOrder(ctx, base)
	if err != nil {
		return nil, mapOrderError(err)
	}

	s.logger.Info("order placed",
		"order_id", created.GetId(),
		"restaurant_id", restaurantID,
		"number", created.GetNumber(),
		"channel", created.GetChannel(),
		"total", created.GetTotal(),
	)
	return domain.OrderViewFromProto(created), nil
}

// priceCart resolves every cart line against the menu: the item must
// exist and be available, the variation (if any) must belong to it, and
// the unit price comes from the menu, never from the client.
func priceCart(menu *menuv1.GetEffectiveMenuResponse, items []CartItem) ([]*orderv1.CreateOrderLine, error) {
	byID := make(map[string]*menuv1.Item)
	for _, cat := range menu.GetCategories() {
		for _, it := range cat.GetItems() {
			byID[it.GetId()] = it
		}
	}

	lines := make([]*orderv1.CreateOrderLine, 0, len(items))
	for _, ci := range items {
		item, ok := byID[ci.ItemID]
		if !ok {
			return nil, domain.NewValidationError("an item in your order is no longer on the menu")
		}
		if !item.GetIsAvailable() {
			return nil, domain.NewValidationError(item.GetName() + " is not available right now")
		}
		if ci.Quantity < 1 || ci.Quantity > 50 {
			return nil, domain.NewValidationError("quantity must be between 1 and 50")
		}

		line := &orderv1.CreateOrderLine{
			ItemId:      item.GetId(),
			ProductCode: item.GetProductCode(),
			Name:        item.GetName(),
			Quantity:    int32(ci.Quantity),
			UnitPrice:   kopecks(item.GetFinalPrice()),
		}

		variation, err := pickVariation(item, ci.VariationID)
		if err != nil {
			return nil, err
		}
		if variation != nil {
			line.VariationId = variation.GetId()
			line.VariationName = variation.GetName()
			line.UnitPrice = kopecks(variation.GetFinalPrice())
		}

		lines = append(lines, line)
	}
	return lines, nil
}

// pickVariation returns the requested variation, the item's default when
// none was requested, or nil for items without variations.
func pickVariation(item *menuv1.Item, variationID string) (*menuv1.Variation, error) {
	vars := item.GetVariations()
	if len(vars) == 0 {
		if variationID != "" {
			return nil, domain.NewValidationError("invalid option for " + item.GetName())
		}
		return nil, nil
	}
	if variationID == "" {
		for _, v := range vars {
			if v.GetIsDefault() {
				return v, nil
			}
		}
		return vars[0], nil
	}
	for _, v := range vars {
		if v.GetId() == variationID {
			return v, nil
		}
	}
	return nil, domain.NewValidationError("invalid option for " + item.GetName())
}

func kopecks(rubles float64) int64 {
	return int64(math.Round(rubles * 100))
}

// ═══════════════════════════════════════════════════════════════════
// READ / KITCHEN
// ═══════════════════════════════════════════════════════════════════

func (s *orderService) ListActive(ctx context.Context, restaurantID string) ([]*domain.OrderView, error) {
	orders, err := s.orders.ListActiveOrders(ctx, restaurantID, nil)
	if err != nil {
		return nil, mapOrderError(err)
	}
	out := make([]*domain.OrderView, 0, len(orders))
	for _, o := range orders {
		out = append(out, domain.OrderViewFromProto(o))
	}
	return out, nil
}

func (s *orderService) UpdateFromKitchen(
	ctx context.Context,
	device *domain.DeviceTokenClaims,
	orderID, newStatus string,
) (*domain.OrderView, error) {
	if !kitchenStatuses[newStatus] {
		return nil, domain.NewValidationError("the kitchen can't set status " + newStatus)
	}

	current, err := s.orders.GetOrder(ctx, orderID)
	if err != nil {
		return nil, mapOrderError(err)
	}
	// Another restaurant's order: behave as if it doesn't exist.
	if current.GetRestaurantId() != device.RestaurantID.String() {
		return nil, domain.NewNotFoundError("order not found")
	}

	updated, err := s.orders.UpdateOrderStatus(ctx, &orderv1.UpdateOrderStatusRequest{
		Id:     orderID,
		Status: newStatus,
		Actor:  "device:" + device.DeviceID.String(),
	})
	if err != nil {
		return nil, mapOrderError(err)
	}
	return domain.OrderViewFromProto(updated), nil
}

func (s *orderService) Get(ctx context.Context, orderID string) (*domain.OrderView, error) {
	o, err := s.orders.GetOrder(ctx, orderID)
	if err != nil {
		return nil, mapOrderError(err)
	}
	return domain.OrderViewFromProto(o), nil
}

// mapOrderError converts kfc-order gRPC errors to domain errors.
func mapOrderError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return domain.NewInternalError(err)
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return domain.NewValidationError(st.Message())
	case codes.NotFound:
		return domain.NewNotFoundError(st.Message())
	case codes.FailedPrecondition, codes.AlreadyExists:
		return domain.NewConflictError(st.Message())
	default:
		return domain.NewInternalError(err)
	}
}
