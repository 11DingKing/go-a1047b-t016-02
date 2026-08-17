# Arctic Express — Booking & Temperature-Control Coordination Backend

A Go backend service for the China–Europe Arctic Express shipping corridor,
coordinating space allocation, dangerous-goods review, reefer temperature
monitoring, customs clearance, and inland delivery for high-value new-energy
cargo (power batteries, energy-storage cabinets, photovoltaic modules).

## Roles & Flow

| Role | Responsibility |
|---|---|
| Freight forwarder | Submits booking application |
| Shipping company space manager | Locks space + completes dangerous-goods review |
| Temperature-control service engineer | Sets reefer parameters for energy-storage & PV cargo |
| Customs broker | Clears goods at destination |
| Consignee warehouse manager | Completes inland delivery within free-storage period |

```
Submit → [space-lock ‖ DG-review] → temp-config → pending-loading
→ loaded → in-transit → arrived → customs-cleared → delivered
```

## Business Rules Implemented

- **FCL-only batteries**: power-battery cargo must be full-container-load; LCL is rejected.
- **2-hour space auto-release**: reserved space not confirmed within 2 h is returned to the pool.
- **Container conflict adjudication**: when two bookings contend for the same reefer container, the earlier declaration wins; the loser is rolled back.
- **15-minute anomaly threshold**: a temperature excursion exceeding 15 min auto-generates an anomaly work order notified to forwarder, ship-owner, and temp-control provider.
- **24-hour port-change window**: port-change requests must arrive ≥ 24 h before departure.
- **Equipment-failure recovery**: a failed reefer triggers automatic backup-container reassignment with a full temperature-curve backtrace for claims.
- **Roll-cargo handling**: bumped cargo is moved to the next voyage with updated ETA and free-storage window.

## Quick Start

```bash
go run ./cmd/server
# Server listens on :56282
```

## Main HTTP Interfaces

| Method | Path | Description |
|---|---|---|
| POST | `/api/bookings` | Submit a booking application |
| GET | `/api/bookings` | List all bookings |
| GET | `/api/bookings/{id}` | Get booking details |
| POST | `/api/bookings/{id}/confirm-space` | Lock vessel space (shipping company) |
| POST | `/api/bookings/{id}/review-dg` | Complete dangerous-goods review |
| POST | `/api/bookings/{id}/configure-temp` | Set reefer parameters (temp-control provider) |
| POST | `/api/bookings/{id}/load` | Load cargo at port |
| POST | `/api/bookings/{id}/depart` | Vessel departure |
| POST | `/api/bookings/{id}/temperature` | Record a temperature reading |
| GET | `/api/bookings/{id}/temperature-curve` | Retrieve full temperature history |
| POST | `/api/bookings/{id}/arrive` | Arrive at destination port |
| POST | `/api/bookings/{id}/customs` | Clear customs |
| POST | `/api/bookings/{id}/deliver` | Complete inland delivery |
| POST | `/api/bookings/{id}/port-change` | Request destination port change |
| POST | `/api/bookings/{id}/roll` | Handle roll-cargo to next voyage |
| POST | `/api/bookings/{id}/equipment-failure` | Handle reefer failure & backup reassignment |
| GET | `/healthz` | Health check |

### Example: submit a booking

```bash
curl -X POST http://localhost:56282/api/bookings \
  -H 'Content-Type: application/json' \
  -d '{
    "forwarder_id": "FF-001",
    "voyage_id": "VG-001",
    "cargo_type": "energy_storage",
    "description": "BESS cabinet 5MWh",
    "load_type": "FCL",
    "weight_kg": 28000,
    "container_id": "CN-REEFER-01",
    "space_id": "SP-001"
  }'
```

## Configuration

| Env var | Default | Description |
|---|---|---|
| `PORT` | `56282` | HTTP listen port |

## Testing

```bash
go test -timeout=120s -count=1 ./...
```

## Docker

```bash
docker build -t arctic-express .
docker run -p 56282:56282 arctic-express
```

The Dockerfile uses multi-stage builds with `golang:1.26` (pinned, not `latest`)
and supports cross-platform builds for `amd64` and `arm64`:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t arctic-express .
```

## Project Structure

```
cmd/server/main.go              HTTP server entry point
internal/domain/                Domain entities & state machine
  cargo.go                      Cargo types & FCL validation
  vessel.go                     Voyage & space
  temperature.go                Temp config, readings, anomalies
  booking.go                    Booking aggregate & state transitions
internal/store/store.go         Concurrency-safe in-memory persistence
internal/app/service.go         Application orchestration
internal/httpapi/handler.go     REST handlers
internal/worker/worker.go       Background: auto-release & anomaly detection
```
