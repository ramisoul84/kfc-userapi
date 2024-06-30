package events

import "time"

const (
	TopicMenuUpdated = "crm.menu.updated"
)

// MenuUpdatedEvent mirrors kfc-crm/internal/events/events.go.
//
// An empty RestaurantID means "all restaurants" — used for global
// menu changes like a master item price update or a new category.
type MenuUpdatedEvent struct {
	RestaurantID string    `json:"restaurant_id"`
	RegionID     string    `json:"region_id,omitempty"`
	Reason       string    `json:"reason"`
	OccurredAt   time.Time `json:"occurred_at"`
}
