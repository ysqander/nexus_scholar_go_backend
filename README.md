## Nexus Scholar – Backend

Production-grade Go backend powering a research assistant that lets users load 20–40 PDFs (plus arXiv IDs), build an aggregated context, and chat with an LLM over that combined corpus. This repo exposes authenticated REST and WebSocket endpoints, manages per-user cache/credits, streams LLM responses, and persists chat history.

### Tech stack
- **Language**: Go 1.21
- **Web framework**: `gin`
- **DB/ORM**: Postgres + `gorm`
- **Auth**: Auth0 (JWT)
- **LLM**: Google AI Studio via `github.com/google/generative-ai-go`
- **Object storage**: Google Cloud Storage (GCS)
- **Payments**: Stripe Checkout/Webhooks
- **Streaming**: WebSockets (`gorilla/websocket`) + SSE for REST streams
- **Logging**: zerolog
- **Container**: Docker + docker-compose

### What it does (high-level)
- Accepts arXiv IDs and/or user-uploaded PDFs to build a single aggregated text cache.
- Stores raw text cache in GCS and creates an LLM cache binding for efficient chat.
- Starts a credit-metered chat session that streams LLM tokens to the client in real-time.
- Persists chats/messages per user; exposes chat history and cache usage.
- Handles credit top-ups via Stripe and updates live clients over WebSocket.

### Project layout
- `cmd/api/main.go` – App entrypoint, wiring, middlewares, service initialization.
- `internal/api/routes.go` – HTTP routes and handlers.
- `internal/services/` – Core domain logic (content aggregation, chat sessions, cache mgmt, GCS, Stripe, etc.).
- `internal/database/db.go` – GORM and migrations.
- `internal/models/` – GORM models.
- `internal/utils/auth/` – Auth0 JWT middleware and `GET /auth/user`.
- `internal/wsocket/` – WebSocket handler for session status and streaming.

### Prerequisites
- Go 1.21+
- Postgres 13+
- Google Cloud project with a service account key that can access GCS
- Stripe account (for payments) – optional if you just want to run core chat without top-ups
- Auth0 application (for JWT validation)

### Environment variables
Create a `.env` file in the project root (used by both `go run` and docker-compose):

```
# App
PORT=3000
GO_ENV=development
ALLOWED_ORIGINS=http://localhost:5173

# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=nexus_scholar

# Google AI Studio
GOOGLE_AI_STUDIO_API_KEY=your_google_ai_studio_api_key

# Google Cloud
GOOGLE_CLOUD_PROJECT=your-gcp-project-id
GCS_BUCKET_NAME=your-gcs-bucket
# One of these must be provided:
# GOOGLE_APPLICATION_CREDENTIALS points to a JSON file path
GOOGLE_APPLICATION_CREDENTIALS=/absolute/path/to/nexus-scholar-key.json
# OR provide inline credentials JSON
# GOOGLE_CREDENTIALS_JSON={...}

# Auth0
AUTH0_DOMAIN=your-tenant.us.auth0.com

# Stripe (optional to test payments)
STRIPE_PUBLIC_KEY=pk_live_or_test
STRIPE_SECRET_KEY=sk_live_or_test
STRIPE_BASE_PRICE_ID=price_xxx
STRIPE_PRO_PRICE_ID=price_yyy
STRIPE_WEBHOOK_SECRET=whsec_xxx
```

Notes:
- When running with Docker, `docker-compose.yml` mounts `nexus-scholar-key.json` into the container at `/app/nexus-scholar-key.json` and sets `GOOGLE_APPLICATION_CREDENTIALS` accordingly.
- In production, prefer `GOOGLE_CREDENTIALS_JSON` or a GCP Workload Identity.

### Running locally (no Docker)
1) Ensure Postgres is running and the DB in `.env` exists.
2) Install Go deps:
```bash
go mod download
```
3) Start the server:
```bash
go run cmd/api/main.go
```

The API will listen on `http://localhost:3000`.

### Running with Docker
Build-and-run via compose:
```bash
docker compose up --build
```

Alternatively, use the Makefile helper:
```bash
make dev
```

This starts Postgres and the backend (hot-reload with `go run` inside the container). The backend binds to `:3000`.

### Key endpoints
All API endpoints require Auth0 JWT unless otherwise noted.

- `GET /auth/user` – Returns the authenticated user object from context.

- `POST /api/create-research-session` – multipart/form-data
  - Form fields:
    - `price_tier`: `base` | `pro`
    - `arxiv_ids`: JSON array string of arXiv IDs, e.g. `["2408.00683","2311.00971"]`
    - `pdfs`: one or more uploaded files
  - Response: `{ cached_content_name, session_id }`

- `POST /api/chat/message` – JSON body `{ session_id, message }`
  - Streams tokens back using Server-Sent Events (SSE). Persisted to chat history.

- `POST /api/chat/terminate` – JSON body `{ session_id }`
  - Gracefully ends the session and finalizes usage metrics.

- `GET /api/chat/history` – Returns prior chats with message timelines and metrics.

- `GET /api/raw-cache?session_id=...` – Returns raw aggregated text for debugging.

- `POST /api/purchase-cache-volume` – JSON `{ price_tier, token_hours }` → Stripe Checkout session id.

- `POST /api/stripe/webhook` – Stripe webhook receiver (set your webhook endpoint to this).

### WebSocket
- `GET /ws?sessionId=...&token=JWT` – Upgrades to a WS connection (JWT can also be provided via `Authorization` for HTTP, but WS uses query `token`).
- Sends periodic session status, low-credit warnings, and credit updates. Accepts messages:
  - `{ type: "message", sessionId, content }` – Send a user message; streams AI tokens back as `{ type: "ai", content }` and terminator `[END]`.
  - `{ type: "terminate", sessionId }` – End session.
  - `{ type: "get_session_status", sessionId }`
  - `{ type: "extend_session", sessionId }`

### Data model (simplified)
- Users, Papers, PaperReferences, Caches, Chats, Messages, TierTokenBudget
- Automatic migrations run at startup via GORM.

### Development notes
- The server logs at debug level unless `GO_ENV=production`.
- CORS defaults to `http://localhost:5173` and can be extended via `ALLOWED_ORIGINS` (comma-separated).
- On first run, the backend will create the GCS bucket if it does not exist.

### Testing
Unit tests exist under `internal/services/tests`. Run with:
```bash
go test ./...
```

### Security & production hardening
- Replace permissive WS `CheckOrigin` with an origin allowlist.
- Use managed secrets and non-root containers.
- Enforce TLS and secure cookie/session handling at the edge.
- Scope service account permissions minimally for GCS.

### License
MIT



