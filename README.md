# kfc-userapi

Customer- and device-facing API for the KFC Russia platform. Serves the
KFC mobile app and every in-restaurant device (kiosks, POS terminals,
kitchen displays).

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

### Device-authenticated

All require `Authorization: Bearer <device-token>`.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/devices/me` | Return the calling device's claims |
| `GET` | `/api/v1/restaurants/:id/menu` | Effective menu for a restaurant |

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
| `pair:meta:{serial}` | JSON | 15m | `{device_id, restaurant_id, expires_at}` |
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
| `CRM_GRPC_ADDRESS` | `localhost:9090` | kfc-crm gRPC (for menu miss) |
| `KAFKA_BROKERS` | — | For consuming `crm.menu.updated` |

See `.env.example` for the full list.

## Running

```bash
cp .env.example .env
make proto-gen
go run ./cmd/userapi