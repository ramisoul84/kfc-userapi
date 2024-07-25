package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	loyaltyv1 "github.com/ramisoul84/kfc-userapi/gen/loyalty/v1"
	orderv1 "github.com/ramisoul84/kfc-userapi/gen/order/v1"
	"github.com/ramisoul84/kfc-userapi/internal/client"
	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// Sign-in limits.
const (
	codeLength          = 6
	maxCodeAttempts     = 5
	resendCooldown      = 60 * time.Second
	codesPerPhoneHour   = 5
	codesPerIPHour      = 20
	kioskLookupsPer10m  = 30
	kioskLookupWindow   = 10 * time.Minute
	refreshTokenEntropy = 32 // bytes
)

// ═══════════════════════════════════════════════════════════════════
// TYPES
// ═══════════════════════════════════════════════════════════════════

type CodeSent struct {
	Phone     string `json:"phone"`
	ExpiresIn int    `json:"expires_in"` // seconds
	ResendIn  int    `json:"resend_in"`  // seconds
	// DevCode is set only when CUSTOMER_DEV_ECHO_CODE is on (no SMS gateway).
	DevCode string `json:"dev_code,omitempty"`
}

// Session is what a successful sign-in or refresh returns. The refresh
// token goes into an httpOnly cookie, never into the JSON body.
type Session struct {
	AccessToken  string         `json:"access_token"`
	ExpiresAt    int64          `json:"expires_at"` // unix seconds
	Member       *domain.Member `json:"member"`
	NewMember    bool           `json:"new_member"`
	RefreshToken string         `json:"-"`
}

type KioskSession struct {
	SessionToken string          `json:"session_token"`
	ExpiresAt    int64           `json:"expires_at"`
	Member       *domain.Member  `json:"member"`
	Program      *domain.Program `json:"program"`
}

// CustomerService covers customer sign-in, the customer's account, and
// identifying a customer at a kiosk.
type CustomerService interface {
	RequestCode(ctx context.Context, rawPhone, ip string) (*CodeSent, error)
	VerifyCode(ctx context.Context, rawPhone, code, firstName string) (*Session, error)
	Refresh(ctx context.Context, refreshToken string) (*Session, error)
	Logout(ctx context.Context, refreshToken string) error

	Me(ctx context.Context, memberID uuid.UUID) (*domain.Member, error)
	UpdateMe(ctx context.Context, memberID uuid.UUID, firstName, email string) (*domain.Member, error)
	Points(ctx context.Context, memberID uuid.UUID, limit, offset int) (*domain.PointsHistory, error)
	Program(ctx context.Context) (*domain.Program, error)
	IssueKioskCode(ctx context.Context, memberID uuid.UUID) (*domain.MemberCode, error)
	Orders(ctx context.Context, memberID uuid.UUID, limit, offset int) ([]*domain.OrderView, int, error)

	IdentifyAtKiosk(ctx context.Context, device *domain.DeviceTokenClaims, code string) (*KioskSession, error)
}

type customerService struct {
	repo         repository.CustomerAuthRepository
	loyalty      *client.LoyaltyClient
	notification *client.NotificationClient
	orders       client.OrderClient
	tokens       *jwt.CustomerTokenManager
	cfg          config.CustomerAuthConfig
	logger       *logger.Logger
}

func NewCustomerService(
	repo repository.CustomerAuthRepository,
	loyalty *client.LoyaltyClient,
	notification *client.NotificationClient,
	orders client.OrderClient,
	tokens *jwt.CustomerTokenManager,
	cfg config.CustomerAuthConfig,
	log *logger.Logger,
) CustomerService {
	return &customerService{
		repo: repo, loyalty: loyalty, notification: notification, orders: orders,
		tokens: tokens, cfg: cfg, logger: log,
	}
}

// ═══════════════════════════════════════════════════════════════════
// SIGN-IN
// ═══════════════════════════════════════════════════════════════════

func (s *customerService) RequestCode(ctx context.Context, rawPhone, ip string) (*CodeSent, error) {
	phone := domain.NormalizePhone(rawPhone)
	if phone == "" {
		return nil, domain.NewValidationError("please enter a valid phone number")
	}

	// Throttles: one SMS per minute and a few per hour per phone, and a
	// cap per client IP so one visitor can't spray many numbers.
	if ok, wait, err := s.repo.Allow(ctx, "send:"+phone, 1, resendCooldown); err != nil {
		return nil, domain.NewInternalError(err)
	} else if !ok {
		return nil, tooManyRequests(fmt.Sprintf("please wait %d s before requesting a new code", int(wait.Seconds())))
	}
	if ok, _, err := s.repo.Allow(ctx, "send-hour:"+phone, codesPerPhoneHour, time.Hour); err != nil {
		return nil, domain.NewInternalError(err)
	} else if !ok {
		return nil, tooManyRequests("too many codes requested for this number, try again later")
	}
	if ok, _, err := s.repo.Allow(ctx, "send-ip:"+ip, codesPerIPHour, time.Hour); err != nil {
		return nil, domain.NewInternalError(err)
	} else if !ok {
		return nil, tooManyRequests("too many requests, try again later")
	}

	code, err := randomDigits(codeLength)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	if err := s.repo.SaveCode(ctx, phone, &repository.LoginCode{Hash: s.hashCode(phone, code)}, s.cfg.CodeTTL); err != nil {
		return nil, domain.NewInternalError(err)
	}
	if err := s.notification.SendLoginCode(ctx, phone, code, s.cfg.CodeTTL); err != nil {
		_ = s.repo.DeleteCode(ctx, phone)
		s.logger.Error("sms send failed", "phone", domain.MaskPhone(phone), "error", err)
		return nil, domain.NewInternalError(errors.New("could not send the SMS"))
	}

	s.logger.Info("sign-in code sent", "phone", domain.MaskPhone(phone))
	out := &CodeSent{
		Phone:     phone,
		ExpiresIn: int(s.cfg.CodeTTL.Seconds()),
		ResendIn:  int(resendCooldown.Seconds()),
	}
	if s.cfg.DevEchoCode {
		out.DevCode = code
	}
	return out, nil
}

func (s *customerService) VerifyCode(ctx context.Context, rawPhone, code, firstName string) (*Session, error) {
	phone := domain.NormalizePhone(rawPhone)
	if phone == "" {
		return nil, domain.NewValidationError("please enter a valid phone number")
	}

	stored, ttl, err := s.repo.GetCode(ctx, phone)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, domain.NewValidationError("the code has expired, please request a new one")
	}
	if err != nil {
		return nil, domain.NewInternalError(err)
	}

	if !hmac.Equal([]byte(stored.Hash), []byte(s.hashCode(phone, code))) {
		stored.Attempts++
		if stored.Attempts >= maxCodeAttempts {
			_ = s.repo.DeleteCode(ctx, phone)
			return nil, domain.NewValidationError("too many wrong attempts, please request a new code")
		}
		if ttl > 0 {
			_ = s.repo.SaveCode(ctx, phone, stored, ttl)
		}
		return nil, domain.NewValidationError(
			fmt.Sprintf("wrong code, %d attempts left", maxCodeAttempts-stored.Attempts))
	}
	_ = s.repo.DeleteCode(ctx, phone)

	resp, err := s.loyalty.UpsertMemberByPhone(ctx, &loyaltyv1.UpsertMemberByPhoneRequest{
		Phone: phone, FirstName: firstName,
	})
	if err != nil {
		return nil, mapOrderError(err)
	}

	sess, err := s.newSession(ctx, domain.MemberFromProto(resp.GetMember()))
	if err != nil {
		return nil, err
	}
	sess.NewMember = resp.GetCreated()
	s.logger.Info("customer signed in", "member_id", sess.Member.ID, "new", sess.NewMember)
	return sess, nil
}

// Refresh rotates the refresh token: the old one stops working.
func (s *customerService) Refresh(ctx context.Context, refreshToken string) (*Session, error) {
	if refreshToken == "" {
		return nil, domain.NewAuthenticationError("not signed in")
	}
	memberID, err := s.repo.TakeRefresh(ctx, hashToken(refreshToken))
	if errors.Is(err, repository.ErrNotFound) {
		return nil, domain.NewAuthenticationError("session expired, please sign in again")
	}
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	m, err := s.Me(ctx, memberID)
	if err != nil {
		return nil, err
	}
	return s.newSession(ctx, m)
}

func (s *customerService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.repo.DeleteRefresh(ctx, hashToken(refreshToken))
}

func (s *customerService) newSession(ctx context.Context, m *domain.Member) (*Session, error) {
	memberID, err := uuid.Parse(m.ID)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	access, exp, err := s.tokens.IssueAccess(memberID)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	raw := make([]byte, refreshTokenEntropy)
	if _, err := rand.Read(raw); err != nil {
		return nil, domain.NewInternalError(err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.repo.SaveRefresh(ctx, hashToken(refresh), memberID, s.cfg.RefreshTTL); err != nil {
		return nil, domain.NewInternalError(err)
	}
	return &Session{AccessToken: access, ExpiresAt: exp.Unix(), Member: m, RefreshToken: refresh}, nil
}

// ═══════════════════════════════════════════════════════════════════
// ACCOUNT
// ═══════════════════════════════════════════════════════════════════

func (s *customerService) Me(ctx context.Context, memberID uuid.UUID) (*domain.Member, error) {
	m, err := s.loyalty.GetMember(ctx, &loyaltyv1.GetMemberRequest{Id: memberID.String()})
	if err != nil {
		return nil, mapOrderError(err)
	}
	return domain.MemberFromProto(m), nil
}

func (s *customerService) UpdateMe(ctx context.Context, memberID uuid.UUID, firstName, email string) (*domain.Member, error) {
	m, err := s.loyalty.UpdateMember(ctx, &loyaltyv1.UpdateMemberRequest{
		Id: memberID.String(), FirstName: firstName, Email: email,
	})
	if err != nil {
		return nil, mapOrderError(err)
	}
	return domain.MemberFromProto(m), nil
}

func (s *customerService) Points(ctx context.Context, memberID uuid.UUID, limit, offset int) (*domain.PointsHistory, error) {
	resp, err := s.loyalty.ListTransactions(ctx, &loyaltyv1.ListTransactionsRequest{
		MemberId: memberID.String(), Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		return nil, mapOrderError(err)
	}
	return domain.PointsHistoryFromProto(resp), nil
}

func (s *customerService) Program(ctx context.Context) (*domain.Program, error) {
	p, err := s.loyalty.GetProgram(ctx, &loyaltyv1.GetProgramRequest{})
	if err != nil {
		return nil, mapOrderError(err)
	}
	return domain.ProgramFromProto(p), nil
}

func (s *customerService) IssueKioskCode(ctx context.Context, memberID uuid.UUID) (*domain.MemberCode, error) {
	c, err := s.loyalty.IssueMemberCode(ctx, &loyaltyv1.IssueMemberCodeRequest{MemberId: memberID.String()})
	if err != nil {
		return nil, mapOrderError(err)
	}
	return &domain.MemberCode{
		Code:      c.GetCode(),
		QRPayload: domain.QRPrefix + c.GetCode(),
		ExpiresAt: time.UnixMilli(c.GetExpiresAt()).UTC(),
	}, nil
}

func (s *customerService) Orders(ctx context.Context, memberID uuid.UUID, limit, offset int) ([]*domain.OrderView, int, error) {
	resp, err := s.orders.ListOrders(ctx, &orderv1.ListOrdersRequest{
		MemberId: memberID.String(), Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		return nil, 0, mapOrderError(err)
	}
	out := make([]*domain.OrderView, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		out = append(out, domain.OrderViewFromProto(o).ToPublic())
	}
	return out, int(resp.GetTotal()), nil
}

// ═══════════════════════════════════════════════════════════════════
// KIOSK
// ═══════════════════════════════════════════════════════════════════

// IdentifyAtKiosk exchanges the code a customer shows (QR or digits) for
// a short session that lets this kiosk apply their points.
func (s *customerService) IdentifyAtKiosk(
	ctx context.Context,
	device *domain.DeviceTokenClaims,
	code string,
) (*KioskSession, error) {
	// 8-digit codes live 5 minutes; this limit makes guessing hopeless.
	if ok, _, err := s.repo.Allow(ctx, "kiosk-identify:"+device.DeviceID.String(), kioskLookupsPer10m, kioskLookupWindow); err != nil {
		return nil, domain.NewInternalError(err)
	} else if !ok {
		return nil, tooManyRequests("too many attempts, please wait a few minutes")
	}

	m, err := s.loyalty.ResolveMemberCode(ctx, &loyaltyv1.ResolveMemberCodeRequest{Code: domain.ParseMemberCode(code)})
	if err != nil {
		return nil, mapOrderError(err)
	}
	member := domain.MemberFromProto(m)
	memberID, _ := uuid.Parse(member.ID)

	token, exp, err := s.tokens.IssueKioskSession(memberID, device.DeviceID)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	program, err := s.Program(ctx)
	if err != nil {
		return nil, err
	}

	s.logger.Info("customer identified at kiosk", "member_id", member.ID, "device_id", device.DeviceID)
	// The kiosk only needs a greeting and the balance.
	return &KioskSession{
		SessionToken: token,
		ExpiresAt:    exp.Unix(),
		Member:       member.ForKiosk(),
		Program:      program,
	}, nil
}

// ═══════════════════════════════════════════════════════════════════
// HELPERS
// ═══════════════════════════════════════════════════════════════════

// hashCode binds the code to the phone and keys it with the server
// secret, so a Redis dump doesn't reveal valid codes.
func (s *customerService) hashCode(phone, code string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	mac.Write([]byte(phone + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomDigits(n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		b[i] = byte('0' + d.Int64())
	}
	return string(b), nil
}

func tooManyRequests(msg string) error {
	return domain.NewTooManyRequestsError(msg)
}
