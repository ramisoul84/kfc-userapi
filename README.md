# kfc-userapi

Customer- and device-facing API for the KFC Russia platform. Serves the
customer website and every in-restaurant device (kiosks, kitchen
displays, hall order displays).

## What it does

- **Menu delivery** — devices and the app fetch the menu for a specific
  restaurant. Served from Redis; on miss, fetched from `kfc-crm` over gRPC
  and cached for 24 hours.
- **Device pairing** — devices submit a pairing code (generated in CRM) and
  receive a long-lived JWT. This is the only way a device authenticates.
- **Device auth** — every subsequent device request carries the JWT;
  userapi verifies it locally (no CRM round-trip).
- **Pairing code cache** — receives codes from CRM over gRPC and stores them
  in Redis. Codes are bcrypt-hashed, single-use, and expire after 15
  minutes.
- **Device revocation** — CRM can revoke a device; userapi adds it to a
  Redis blocklist so its tokens stop working before their natural expiry.
- **Device types** — the token carries the device type (`kiosk`, `kds`,
  `order_display`) and each endpoint accepts only the types that should
  use it.
- **Ordering gateway** — kiosks and the website submit carts here.
  userapi prices every line from the restaurant's effective menu (clients
  never send prices) and creates the order in `kfc-order`.
- **Live order screens** — consumes `orders.events` from Kafka and pushes
  them over SSE to the kitchen display, the hall display (numbers only)
  and the customer's tracking page.
- **Restaurant directory** — public city/restaurant lists for the website,
  read from CRM over gRPC.
- **Customer sign-in** — phone number + one-time SMS code (sent through
  kfc-notification). Issues a 15-minute access token and a rotating
  30-day refresh token in an httpOnly cookie. The customer *is* a
  loyalty member in kfc-loyalty.
- **Loyalty at checkout** — website customers and kiosk customers who
  scanned their code can pay part of an order with points (two-phase
  hold in kfc-loyalty, see below).

## Architecture
Kiosk / App ──HTTP──▶┌────────────────────────┐
│ │
CRM ──gRPC──────────▶│ kfc-userapi │
│ │
└──────┬──────────┬──────┘
│ │
┌──────▼───┐ ┌───▼─────┐
│ Redis │ │ CRM │
│ (cache + │ │ (gRPC │
│ codes + │ │ on │
│ revoke) │ │ miss) │
└──────────┘ └─────────┘
▲
│
Kafka: crm.menu.updated
→ invalidate menu cache


**Redis is the primary store** for menu caches, pairing codes, and
revocation entries. CRM's Postgres is the source of truth for menus and
device registration; userapi treats Redis as a fast front-end with CRM as
the fallback.

## Key flows

### Menu fetch (kiosk/app)

1. Device sends `GET /api/v1/restaurants/:id/menu` with its Bearer token.
2. `DeviceAuth` middleware verifies the JWT signature and checks the
   revocation list.
3. Handler checks Redis for `menu:restaurant:{id}`.
   - **Hit** → return immediately.
   - **Miss** → singleflight → gRPC call to CRM → cache with 24h TTL.
4. Response is JSON.

### Device pairing

1. Device sends `POST /api/v1/devices/pair` with `{serial_number, code}`.
2. `DeviceAuthService.Pair` loads the bcrypt hash from Redis.
3. `bcrypt.CompareHashAndPassword` verifies the code.
4. The code is deleted (single-use).
5. A JWT is issued with claims `{device_id, serial_number, restaurant_id,
   jti, iat, nbf, exp}`.
6. Response includes the token; the device stores it (`0600` file) and uses
   it on every subsequent request.

### Menu invalidation

1. Admin updates a menu in CRM.
2. CRM publishes `crm.menu.updated` to Kafka with `{restaurant_id}`.
3. userapi's consumer deletes `menu:restaurant:{id}` from Redis.
4. Next request refetches from CRM.

### Device revocation

1. Manager deletes or deactivates a device in CRM.
2. CRM calls `RevokeDevice` over gRPC.
3. userapi writes `revoked:device:{id}` to Redis with TTL matching the
   token lifetime.
4. Every subsequent device request checks this key; revoked devices get
   `401` immediately, not after their token expires.

## Endpoints

### Public

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Liveness probe |
| `POST` | `/api/v1/devices/pair` | Exchange a pairing code for a device token |
| `GET` | `/api/v1/restaurants/:id/menu` | Effective menu for a restaurant |
| `GET` | `/api/v1/restaurants/:id/menu/stream` | SSE: `menu-updated` |
| `GET` | `/api/v1/public/regions` | Cities that have restaurants |
| `GET` | `/api/v1/public/restaurants?region_id=` | Active restaurants |
| `GET` | `/api/v1/public/restaurants/:id` | One restaurant |
| `GET` | `/api/v1/public/orders/:id` | Order tracking |
| `GET` | `/api/v1/public/orders/:id/stream` | SSE: updates for that order only |

### Customers (website)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/customer/auth/code` | Text a 6-digit sign-in code `{phone}` |
| `POST` | `/api/v1/customer/auth/verify` | `{phone, code, first_name?}` → access token + refresh cookie |
| `POST` | `/api/v1/customer/auth/refresh` | Rotate the refresh cookie, new access token |
| `POST` | `/api/v1/customer/auth/logout` | Revoke the refresh token |
| `GET`/`PATCH` | `/api/v1/customer/me` | Profile and points balance |
| `GET` | `/api/v1/customer/me/points` | Points history |
| `POST` | `/api/v1/customer/me/code` | Fresh kiosk code `{code, qr_payload, expires_at}` |
| `GET` | `/api/v1/customer/me/orders` | Order history |
| `POST` | `/api/v1/customer/restaurants/:id/orders` | Checkout (signed in; optional `redeem_points`) |
| `GET` | `/api/v1/public/loyalty/program` | Earn/redeem rules |

Sign-in protections: one code per minute and 5 per hour per phone, 20 per
hour per IP; codes HMAC-hashed and bound to the phone, 5 attempts, 5 min
lifetime. Refresh tokens are stored hashed and work once.

### Loyalty at the kiosk

1. The customer opens *My code* on the website: a QR code
   (`KFC-LOYALTY:<8 digits>`) plus the digits, valid 5 min, single use.
2. The kiosk sends it to `POST /api/v1/kiosk/loyalty/identify` (from the
   camera, a hardware scanner or the keypad; max 30 lookups / 10 min per
   kiosk) and receives a 10-minute `session_token` bound to that kiosk.
3. The kiosk includes `loyalty_session` and `redeem_points` in
   `POST /kiosk/orders`. Points are earned on the order either way.

### Points at checkout

`redeem_points` → `ReservePoints` (hold, capped by the program) → create
the order with the discount and hold id → `CommitHold`. A definite
failure releases the hold; an ambiguous one (timeout) leaves it for the
`order.created` event to commit, or for it to expire.

### Device-authenticated

All require `Authorization: Bearer <device-token>`.

| Method | Path | Device types | Purpose |
|---|---|---|---|
| `GET` | `/api/v1/devices/me` | any | Device identity + restaurant name |
| `POST` | `/api/v1/kiosk/orders` | `kiosk` | Place an order (paid by card on the terminal) |
| `POST` | `/api/v1/kiosk/loyalty/identify` | `kiosk` | Exchange a customer's code for a loyalty session |
| `GET` | `/api/v1/kitchen/orders` | `kds` | Active orders, oldest first |
| `POST` | `/api/v1/kitchen/orders/:id/status` | `kds` | `preparing` / `ready` / `completed` / back to `accepted` |
| `GET` | `/api/v1/display/orders` | `order_display` | Numbers and statuses only |
| `GET` | `/api/v1/device/orders/stream` | `kds`, `order_display` | SSE: `order` events for the device's restaurant |

### Order checkout body

```json
{
  "order_type": "dine_in",
  "idempotency_key": "6f1c…",          // kiosk: one per checkout, safe to retry
  "customer_name": "Anna",              // website only
  "customer_phone": "+7 999 123-45-67", // website only
  "items": [
    { "item_id": "…", "variation_id": "…", "quantity": 2 }
  ]
}
```

### Live order stream contract

1. Connect to the stream and wait for the `connected` event.
2. Fetch the full list over HTTP.
3. Apply each `order` event (`{type, order}`) as an upsert by `order.id`,
   ignoring events older than the `updated_at` you already have.
4. On reconnect, repeat from 1. Nothing is replayed; the refetch covers
   the gap.

Each userapi instance consumes `orders.events` in its own consumer group
(`<KAFKA_GROUP_ID>-orders-<hostname>`) starting at the newest offset,
because SSE clients are spread across instances and all of them need
every event.

### gRPC (internal, called by CRM)

| RPC | Purpose |
|---|---|
| `CachePairingCodes` | Batch-store pairing codes |
| `CachePairingCode` | Store a single code |
| `RevokePairingCode` | Delete a code by serial |
| `RevokeDevice` | Block all tokens for a device |

## Redis key schema

| Key | Type | TTL | Purpose |
|---|---|---|---|
| `pair:code:{serial}` | string | 15m | bcrypt hash of the pairing code |
| `pair:meta:{serial}` | JSON | 15m | `{device_id, device_type, restaurant_id, expires_at}` |
| `pair:restaurant:{id}` | set | 15m | Serials belonging to this restaurant's batch |
| `menu:restaurant:{id}` | JSON | 24h | Cached effective menu |
| `revoked:device:{id}` | string | 7d | Device revoked; matches token TTL |
| `revoked:token:{jti}` | string | 7d | Single token revoked |

**Redis must have AOF persistence enabled** (`appendonly yes`,
`appendfsync everysec`). Pairing codes live only in Redis — a restart
without persistence loses them mid-window, and every restaurant's morning
setup would have to be redone.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `APP_ENV` | `development` | `development` / `staging` / `production` |
| `HTTP_PORT` | `8001` | HTTP server port |
| `GRPC_PORT` | `9102` | gRPC server port |
| `REDIS_*` | — | Redis connection (must include AOF persistence) |
| `JWT_DEVICE_SECRET` | — | HMAC secret for device tokens (≥32 chars in prod) |
| `JWT_DEVICE_TTL` | `168h` | Device token lifetime (7 days) |
| `JWT_ISSUER` | `kfc-userapi` | `iss` claim |
| `CRM_GRPC_ADDRESS` | `localhost:9090` | kfc-crm gRPC (menu, restaurant directory) |
| `ORDER_GRPC_ADDRESS` | `localhost:9103` | kfc-order gRPC |
| `KAFKA_BROKERS` | — | For consuming `crm.menu.updated` and `orders.events` |
| `CORS_ALLOW_ORIGINS` | kiosk, website, KDS, display dev ports | Browser origins |
| `PUBLIC_ORDER_RATE_LIMIT` | `10` | Web checkouts per IP per minute |

See `.env.example` for the full list.

## Running

```bash
cp .env.example .env
make proto-gen
go run ./cmd/userapi