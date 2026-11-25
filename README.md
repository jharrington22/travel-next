# Travel Next

Plan a trip through a short form, have an LLM turn it into a day-by-day
itinerary, and publish it as a single shareable page at
`/u/<username>/<trip>` — the link you send to the family or friends you're
visiting.

Go + Gin, Postgres, cookie sessions for the browser and bearer tokens for the
API, an OpenAPI 3.1 contract, a pluggable AI backend (Anthropic, any
OpenAI-compatible endpoint, a local model, or an offline stub), an nginx front
end, and Kubernetes manifests that run on a laptop cluster.

---

## Contents

- [How it works](#how-it-works)
- [Repository layout](#repository-layout)
- [Quick start](#quick-start-30-seconds-no-ai-key-needed)
- [Build](#build)
- [Configuration](#configuration)
  - [AI providers](#ai-providers)
  - [Hero photos (optional, costs no AI tokens)](#hero-photos-optional-costs-no-ai-tokens)
  - [Prompt context: tuning the AI without a code change](#prompt-context-tuning-the-ai-without-a-code-change)
- [Running locally](#running-locally)
  - [1. Go process + Postgres in podman](#1-go-process--postgres-in-podman)
  - [2. Whole stack in podman (nginx + app + Postgres)](#2-whole-stack-in-podman-nginx--app--postgres)
- [Running on Kubernetes](#running-on-kubernetes)
- [The API](#the-api)
- [The translation layer](#the-translation-layer)
- [Database](#database)
- [Security notes](#security-notes)
- [Tests](#tests)
- [Operations](#operations)

---

## How it works

```
browser ──▶ nginx ──▶ Gin app ──▶ Postgres
                        │
                        ├─▶ AI provider (Anthropic | OpenAI-compatible | local | stub)
                        └─▶ prompt context (file locally, ConfigMap in k8s)
```

1. A signed-in user fills in the trip form (`/new`).
2. The app stores the request, returns immediately, and generates in the
   background. The row's `status` moves `generating` → `ready` (or `failed`).
3. Generation combines three things: the **form answers**, the **standing
   context** from `prompt-context.yaml`, and a **JSON schema**. The provider is
   asked for structured JSON — not prose — so the result can be stored,
   re-rendered and served over the API.
4. The JSON is rendered server-side into one interactive page: day tabs, a
   timeline per day, lodging, food, transport, a rough budget, a packing
   checklist and tips. It prints cleanly and has no third-party assets.
5. The same itinerary is available over the REST API under `/api/v1`.

The generation call is detached from the HTTP request, so closing the tab does
not cancel it. The itinerary page refreshes itself while `status` is
`generating`.

## Repository layout

```
cmd/server/              main: config, DB wait + migrate, wiring, graceful shutdown
internal/
  config/                environment configuration
  models/                 internal domain types
  store/                  Postgres access + embedded SQL migrations
  auth/                   bcrypt passwords, DB-backed sessions, API tokens, CSRF, middleware
  promptctx/              operator-tunable AI context (the ConfigMap), hot-reloaded
  ai/                     Generator interface + anthropic / openai-compatible / stub backends
  api/v1/                 the PUBLIC API contract (DTOs) and the translation layer
  server/                 routes, web handlers, API handlers, templates, static assets, openapi.yaml
deploy/
  nginx/nginx.conf        local front end (the ingress does this job in k8s)
  k8s/                    namespace, config, secret, Postgres, app, ingress, kind config
  k8s/prompt-context.yaml the prompt context — read directly when local, ConfigMap in k8s
Dockerfile               multi-stage build (works unchanged with podman)
compose.yaml             nginx + app + Postgres for podman-compose
Makefile                 every workflow below, as a target
```

## Quick start (30 seconds, no AI key needed)

```bash
make db-up          # Postgres in podman
make run            # app on http://localhost:8080 with AI_PROVIDER=stub
```

Open http://localhost:8080, create an account, and plan a trip. With
`AI_PROVIDER=stub` the itinerary is an obviously-placeholder plan, so the whole
flow — signup, form, background generation, public page, API — works before you
configure a model. Set a provider when you want real plans.

## Build

Requirements: Go 1.26+ (or 1.24 with toolchain downloads enabled), Postgres 16 (or podman), and podman if you want the
container image.

```bash
make test           # go test ./...
make vet            # go vet ./...
make build          # static binary at ./bin/server
make image          # container image localhost/travel-next:dev (podman build)
```

The image is a two-stage build: `golang:1.26-alpine` compiles a static
`CGO_ENABLED=0` binary, and the runtime stage is `alpine:3.21` running as
non-root UID 10001 with a read-only root filesystem in Kubernetes. Templates,
CSS, JS, the SQL migrations, the OpenAPI spec and the default prompt context are
all embedded in the binary with `go:embed`, so the image has no asset layout to
get wrong.

`Dockerfile` needs no changes for podman (`podman build -f Dockerfile`), and
nothing in this repo requires a Docker daemon.

## Configuration

Everything is environment variables. `.env.example` is a copyable starting
point; for local runs:

```bash
cp .env.example .env
set -a; . ./.env; set +a
make run
```

### HTTP

| Variable | Default | Notes |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `BASE_URL` | `http://localhost:8080` | Used to build shareable links and API `Location` headers |
| `APP_ENV` | `development` | Anything else switches Gin to release mode and raises the log level |
| `COOKIE_SECURE` | `false` | Set `true` once you serve HTTPS |
| `TRUSTED_PROXIES` | *(none)* | Comma-separated CIDRs whose `X-Forwarded-*` headers are trusted. Empty means trust none |
| `SESSION_TTL` | `720h` | Session cookie and DB row lifetime |

### Database

| Variable | Default | Notes |
|---|---|---|
| `DATABASE_URL` | *(none)* | `postgres://user:pass@host:5432/db?sslmode=disable` |
| `PGHOST` `PGPORT` `PGUSER` `PGPASSWORD` `PGDATABASE` `PGSSLMODE` | — | Used to compose a URL when `DATABASE_URL` is empty, which is how the Kubernetes manifests supply credentials (password from a Secret, the rest from a ConfigMap) |

One of the two is required; the app refuses to start without a database.

### Limits

| Variable | Default | Notes |
|---|---|---|
| `DAILY_GENERATION_LIMIT` | `25` | Itineraries per user per rolling 24h |
| `GENERATION_TIMEOUT` | `10m` | Hard ceiling on one background generation |
| `PROMPT_CONTEXT_FILE` | `deploy/k8s/prompt-context.yaml` | See below |
| `UNSPLASH_ACCESS_KEY` | *(none)* | Optional. Enables a hero photo per itinerary — see [Hero photos](#hero-photos-optional-costs-no-ai-tokens) |

### AI providers

The generator is an interface (`internal/ai.Generator`) with four
implementations, selected by `AI_PROVIDER`:

| `AI_PROVIDER` | Backend | Required settings |
|---|---|---|
| `anthropic` | Anthropic Messages API via the official Go SDK, with structured outputs, adaptive thinking and streaming | `ANTHROPIC_API_KEY` (or an `ant auth login` profile / `ANTHROPIC_AUTH_TOKEN`); `AI_MODEL` defaults to `claude-opus-5` |
| `openai` | OpenAI chat completions with strict `json_schema` | `AI_API_KEY` (or `OPENAI_API_KEY`); `AI_MODEL` defaults to `gpt-4o`; `AI_BASE_URL` defaults to `https://api.openai.com/v1` |
| `local` | The same chat-completions protocol against a local server — Ollama, llama.cpp, vLLM, LM Studio | `AI_BASE_URL` defaults to `http://localhost:11434/v1`; set `AI_MODEL` (e.g. `llama3.1`). No key needed |
| `stub` | Deterministic placeholder itinerary, no network | none |

Shared knobs: `AI_MODEL`, `AI_MAX_TOKENS` (default 32000), `AI_TIMEOUT`
(default `10m`), and `AI_EFFORT` (`low`…`max`, Anthropic only).

Examples:

```bash
# Anthropic
AI_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... make run

# OpenAI
AI_PROVIDER=openai AI_API_KEY=sk-... AI_MODEL=gpt-4o make run

# Local model via Ollama
ollama serve & ollama pull llama3.1
AI_PROVIDER=local AI_MODEL=llama3.1 AI_BASE_URL=http://localhost:11434/v1 make run
```

Both hosted paths ask for schema-validated JSON. Against a local runner that
does not implement strict `json_schema`, the request is retried once in
JSON-object mode with the schema inlined in the prompt, and the response is
repaired (markdown fences stripped, missing scalars filled from the request)
before it is stored — small models need that slack.

### Hero photos (optional, costs no AI tokens)

Set `UNSPLASH_ACCESS_KEY` (a free key from
[unsplash.com/developers](https://unsplash.com/developers)) to put a photo
behind each itinerary's hero banner:

```bash
UNSPLASH_ACCESS_KEY=... make run
```

The lookup runs once per itinerary, after the model responds, searching
Unsplash by the `destination` text the model already returned — it is a plain
HTTP call, not an AI call, so it adds nothing to the token bill. We never
download or re-host the photo: only its direct `images.unsplash.com` URL and
the photographer attribution Unsplash's API terms require are stored on the
itinerary (`hero_photo` in the JSON document), and the page hotlinks it —
Unsplash's preferred usage, not merely a tolerated one. Leave the key unset
and itineraries render with the plain color-gradient banner instead; nothing
else changes. The free tier allows 50 searches/hour, plenty for one lookup per
itinerary created.

Because the photo is genuinely optional, the CSP in `securityHeaders()`
(`internal/server/server.go`) allows exactly one additional image host,
`https://images.unsplash.com`, and nothing else — `style-src`/`script-src`
stay `'self'` with no `'unsafe-inline'`; the photo's background-image is set
by `static/js/itinerary.js` from a `data-hero-photo` attribute rather than an
inline `style=""`, specifically so it doesn't need a looser `style-src`.

### Prompt context: tuning the AI without a code change

`deploy/k8s/prompt-context.yaml` holds everything about *how* the model is
asked, separately from the code that asks:

```yaml
system_prompt: |     # the base instruction block
sections:            # headed guidance blocks, appended in order
  - heading: How to plan
    body: |
      ...
rules:               # short imperative lines, appended as a bulleted list
user_preamble:       # prepended to the per-request message
form:                # the vocabulary the FORM offers
  trip_types: [...]
  paces: [...]
  interests: [...]
limits:              # max nights, max travellers, items per day
```

Two things make this worth knowing about:

- **`form:` drives the actual form.** The dropdowns and checkboxes on `/new`,
  and the `GET /api/v1/form-options` response, are rendered from this file. The
  words a user picks and the words the model is prompted with cannot drift
  apart.
- **It is hot-reloaded.** The file is re-read when its contents change (checked
  at most every 10s). In Kubernetes it is mounted from a ConfigMap, so
  `kubectl edit configmap travel-next-prompt-context` changes the prompt on
  running pods within about 70 seconds — no rebuild, no restart, no rollout.

One file serves both environments: the app reads it directly when you run
locally, and `kustomize` turns the same file into the ConfigMap.
`internal/promptctx/default.yaml` is a copy compiled into the binary and used
only as a fallback when `PROMPT_CONTEXT_FILE` is missing — `/readyz` says so
when that happens, so a mis-mounted volume is visible rather than silent.
`/readyz` reports which copy is live:

```json
{"status":"ok","database":"ok","generator":"anthropic/claude-opus-5",
 "prompt_context":{"source":"/etc/travel-next/prompt-context.yaml","checksum":"88c58b62","version":1}}
```

A file that exists but does not parse is a **startup error** — booting with
silently-wrong prompt context is worse than failing loudly. An unknown key is
also an error, so a ConfigMap typo can't quietly do nothing. If a *reload*
fails, the last good copy stays in use and the error shows up in `/readyz` as
`prompt_context_error`.

## Running locally

### 1. Go process + Postgres in podman

Fastest loop for development.

```bash
make db-up                      # podman run postgres:16-alpine on :5432
make run                        # go run ./cmd/server
# ... edit, Ctrl-C, make run ...
make db-down                    # remove the container when finished
```

Migrations run automatically at startup, so there is no separate migrate step.

### 2. Whole stack in podman (nginx + app + Postgres)

This is the same request path as production — client → nginx → app → Postgres:

```bash
make compose-up                 # podman-compose up --build -d
make compose-logs
# http://localhost:8080
make compose-down
```

To use a real provider, export the variables before bringing it up — the
compose file passes them through:

```bash
export AI_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-...
make compose-up
```

`deploy/k8s/prompt-context.yaml` is bind-mounted into the app container, so
editing it on the host retunes the running stack.

> Rootless podman cannot bind ports below 1024 by default, which is why the
> local front end listens on 8080 rather than 80.

## Running on Kubernetes

Everything is in `deploy/k8s` and applied with `kubectl apply -k`. Written for
**kind on podman**; prerequisites are `kubectl`, `kind` (or `minikube`), and a
kubectl context pointing at that local cluster:

```bash
# Fedora/RHEL
sudo dnf install kubernetes-client
go install sigs.k8s.io/kind@latest      # or: dnf install kind
```

`make kind-up` exports `KIND_EXPERIMENTAL_PROVIDER=podman` for you, so kind
drives podman rather than looking for a Docker daemon.

```bash
make kind-up          # kind create cluster (KIND_EXPERIMENTAL_PROVIDER=podman), ports 80/443 mapped
make kind-ingress     # install ingress-nginx and wait for the controller
make kind-load        # podman build + podman save + kind load image-archive
make k8s-deploy       # kubectl apply -k deploy/k8s, then wait for the rollout
# open http://travel-next.localtest.me   (*.localtest.me always resolves to 127.0.0.1)
```

What gets created, in order:

| File | Contents |
|---|---|
| `00-namespace.yaml` | namespace `travel-next` |
| `10-secrets.yaml` | **example** Secret: Postgres password, `ANTHROPIC_API_KEY`, `AI_API_KEY` |
| `20-config.yaml` | ConfigMap `travel-next-env`: all non-secret settings, including `AI_PROVIDER` |
| `30-postgres.yaml` | headless Service + single-replica StatefulSet with a 2Gi PVC and a `pg_isready` probe |
| `40-app.yaml` | Service + 2-replica Deployment: non-root, read-only root filesystem, all capabilities dropped, startup/readiness/liveness probes, prompt-context ConfigMap mounted at `/etc/travel-next` |
| `50-ingress.yaml` | the nginx front end as an Ingress (`ingressClassName: nginx`, host `travel-next.localtest.me`) |
| `kustomization.yaml` | ties them together and generates the prompt-context ConfigMap from `prompt-context.yaml`, with a stable name (no hash suffix) so editing it in-cluster is a normal operation |

**Replace the example secret before anything that matters.** Either edit
`10-secrets.yaml`, or delete it from `kustomization.yaml` and create the secret
directly:

```bash
kubectl -n travel-next create secret generic travel-next \
  --from-literal=POSTGRES_PASSWORD="$(openssl rand -base64 24)" \
  --from-literal=ANTHROPIC_API_KEY='sk-ant-...' \
  --from-literal=AI_API_KEY=''
```

Then point it at a provider and roll:

```bash
kubectl -n travel-next set env deploy/travel-next AI_PROVIDER=anthropic AI_MODEL=claude-opus-5
# or edit 20-config.yaml and re-apply
```

Other targets: `make k8s-status`, `make k8s-logs`, `make port-forward` (reach
the Service without the ingress), `make k8s-prompt-reload` (push a local
prompt-context edit with no restart), `make k8s-delete`, `make kind-down`.

**minikube** works too — `minikube start --driver=podman`,
`minikube addons enable ingress`, `minikube image load localhost/travel-next:dev`,
then `make k8s-deploy`. Point the host at `minikube ip` instead of using
`localtest.me`.

Notes on the manifests:

- `imagePullPolicy: IfNotPresent` with a `localhost/` image: the cluster never
  tries to pull from a registry. Rebuild and re-load with `make kind-load`,
  then `kubectl -n travel-next rollout restart deploy/travel-next`.
- Migrations run in-process at startup and are idempotent, so a rolling update
  needs no Job or init container; `maxUnavailable: 0` plus the startup probe
  keeps the old pods serving until a new one is actually ready.
- A single Postgres replica with a PVC is deliberate — it is the simple thing
  for a laptop cluster. For anything real, point `PGHOST` at a managed Postgres
  and delete `30-postgres.yaml`.

## The API

Authenticated, versioned under `/api/v1`, and described by OpenAPI 3.1 at
**`/api/openapi.yaml`** (human summary at `/api/docs`). Point any generator at
the spec.

Authenticate with a bearer token created at `/settings` in the UI, or via
`POST /api/v1/tokens` with a browser session. Tokens are stored as SHA-256
hashes and shown once. A browser session also authenticates API calls, which is
what makes the docs page usable while signed in.

```bash
TOKEN=tnx_...

curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/me

curl -X POST http://localhost:8080/api/v1/itineraries \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"destination":"Kyoto, Japan","start_date":"2026-04-02","end_date":"2026-04-09",
       "travellers":2,"pace":"balanced","budget":"mid-range","currency":"AUD",
       "interests":["food and markets"],"audience":"family at home"}'
# 202 Accepted + Location; poll until status leaves "generating"
```

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/me` | The authenticated user |
| `GET` | `/api/v1/form-options` | Vocabulary from the prompt-context ConfigMap |
| `GET` | `/api/v1/itineraries` | List yours (`limit`, `offset`) |
| `POST` | `/api/v1/itineraries` | Create one; `202` while it generates |
| `GET` | `/api/v1/itineraries/{id}` | Fetch one, including the generated document |
| `PATCH` | `/api/v1/itineraries/{id}` | `{"public": true|false}` |
| `DELETE` | `/api/v1/itineraries/{id}` | Delete |
| `GET`/`POST` | `/api/v1/tokens` | List / create API tokens |

Errors always use one envelope:

```json
{"error":{"code":"invalid_request","message":"end_date must not be before start_date"}}
```

Unauthenticated API calls get a JSON `401` (never an HTML redirect); an
itinerary belonging to someone else is reported as `404`, so the endpoint does
not confirm which ids exist.

Unauthenticated endpoints: `/healthz`, `/readyz`, `/api/openapi.yaml`,
`/api/docs`, and public itinerary pages.

## The translation layer

`internal/api/v1` is the public contract, and it is **not** the domain model.
`dto.go` defines the wire shapes; `translate.go` is the only place where the two
meet. API handlers never touch `internal/models` except through those
functions.

Why it is worth the extra file:

- The internal model can be refactored — renamed fields, a different shape, a
  split type — without changing a byte of what published clients receive.
- A breaking wire change has to be made *deliberately*, by editing `dto.go`
  (and, properly, adding a `v2` package), rather than leaking out of an
  unrelated change.
- `openapi.yaml` describes the DTOs, so the spec and the structs stay in step.

Compatibility is enforced by tests rather than by discipline
(`internal/api/v1/translate_test.go`):

- `TestDocumentRoundTripFromInternal` / `...FromWire` use reflection to fill
  **every** field of the document on each side with a distinct value, convert
  out and back, and require deep equality. Add a field to either side without a
  mapping and the round trip loses the value — the test fails and names the
  field.
- `TestTripRequestRoundTrip` does the same for the request body.
- `fill()` fails loudly on an unhandled kind, so adding e.g. a map or a
  `time.Time` to a DTO forces a decision rather than silently passing.
- In `internal/server`, `TestOpenAPISpecMatchesRoutes` diffs the registered Gin
  routes under `/api/v1` against the paths in `openapi.yaml` in both
  directions: an endpoint added without a spec entry fails, and so does a spec
  entry with no endpoint.

So the three things that must agree — internal model, public DTOs, published
spec — are each pinned by a test.

## Database

Postgres, accessed with `pgx/v5` through a pool. Migrations are embedded SQL in
`internal/store/migrations`, applied at startup inside a transaction and
recorded in `schema_migrations`, so they run once and are safe to repeat across
replicas and restarts.

Tables: `users`, `sessions`, `api_tokens`, `itineraries` (the trip request and
the generated document are `jsonb`), `schema_migrations`. Add a migration by
dropping a new `000N_name.sql` into that directory — files apply in filename
order.

## Security notes

- Passwords: bcrypt (`DefaultCost`). A login for an unknown user still pays a
  comparable bcrypt cost, so response timing does not reveal which accounts
  exist.
- Sessions: 32 random bytes from `crypto/rand`, stored server-side, sent as an
  `HttpOnly`, `SameSite=Lax` cookie (add `Secure` with `COOKIE_SECURE=true`).
  Expired rows are reaped hourly.
- CSRF: a per-session token is required on every browser form post. Bearer-token
  callers are exempt, because a token is never sent automatically by a browser.
- API tokens: `tnx_` + 32 random bytes, stored as SHA-256, displayed once, with
  a visible prefix for identification and per-token revocation.
- Headers: `nosniff`, `X-Frame-Options: DENY`, a referrer policy, and a strict
  `Content-Security-Policy` on HTML routes. The one deliberate exception is
  `img-src https://images.unsplash.com` for the optional hero photo (see
  [Hero photos](#hero-photos-optional-costs-no-ai-tokens)) — `style-src` and
  `script-src` stay `'self'` with no `'unsafe-inline'`.
- Prompt injection: the traveller's free-text notes are passed as data and
  labelled as such in the prompt, and the generated document is rendered through
  `html/template`'s contextual escaping — the model's output is never
  interpolated as markup. Generated pages contain no links.
- Trusted proxies default to none; set `TRUSTED_PROXIES` to the CIDRs of your
  ingress so client IPs in logs are not attacker-controlled.
- Private itineraries are visible only to their owner.

## Tests

```bash
make test
```

What is covered without any external dependency: the translation-layer round
trips, the spec-versus-routes diff, HTML rendering of every public page (a
template typo fails the build, not a user's page), the unauthenticated 401/303
split, slug generation, prompt-context parsing and fallback behaviour, the
strict-mode safety of the generated JSON schema, and the stub generator's day
coverage. There is no database or network in the test path.

## Operations

| Endpoint | Use |
|---|---|
| `GET /healthz` | Liveness: process is up |
| `GET /readyz` | Readiness: database reachable, which generator is active, prompt-context source/checksum, last reload error |

Logs are JSON (`log/slog`) on stdout: one line per request, plus
`itinerary generated` / `itinerary generation failed` with the elapsed time and
the generator name. `SIGTERM` drains in-flight requests for up to 20s.

Common things to check:

- *Itineraries stuck on `generating`* — look for `itinerary generation failed`
  in the logs; a failure also lands in the row's `error` field and is shown on
  the page. Credentials, model name and `AI_BASE_URL` are the usual causes.
- *Prompt changes not taking effect* — compare the `prompt_context.checksum` in
  `/readyz` before and after; check `prompt_context_error`.
- *`database not ready` at startup* — the app waits up to 60s for Postgres, then
  exits so the orchestrator can retry.
