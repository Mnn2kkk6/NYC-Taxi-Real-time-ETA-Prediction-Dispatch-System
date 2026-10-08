# Phase 7 — Frontend (React + Vite) — DONE

A dispatch-console UI for the Go backend: predict a trip, watch live
events, browse trips, and see drivers on a map.

## Folder

```
frontend/
├── index.html
├── package.json
├── vite.config.js
├── .env.example                  # VITE_API_BASE_URL
└── src/
    ├── main.jsx
    ├── App.jsx                   # routes
    ├── index.css                 # design tokens + all styles
    ├── liveEventsContext.js      # shares ONE websocket across pages
    ├── api/client.js             # every REST call to the Go backend
    ├── hooks/
    │   ├── useLiveEvents.js      # /ws connection, auto-reconnect w/ backoff
    │   └── useSystemStatus.js    # polls /api/v1/system/status every 2s
    ├── components/Layout.jsx     # sidebar + live/reconnecting indicator
    └── pages/
        ├── PredictionPage.jsx    # form + ETA result
        ├── DashboardPage.jsx     # stats, queue/worker chart, live event feed
        ├── TripsPage.jsx         # table, status filter, new-row highlight
        └── MapPage.jsx           # Leaflet: drivers by status + trip lines
```

## Pages

| Route | What it does | Backend calls |
|---|---|---|
| `/` | Trip form (with 3 NYC presets) → ETA in minutes, distance, model, cache hit/miss, request ID | `POST /api/v1/predict` |
| `/dashboard` | Jobs waiting / busy workers / cache hit rate, a rolling chart, and a live event feed | `GET /api/v1/system/status` (poll), `/ws` |
| `/trips` | 50 most recent trips, filter by status, refetches when a `trip_created` event arrives | `GET /api/v1/trips`, `/ws` |
| `/map` | Drivers colored by status, trip pickup points and pickup→dropoff lines | `GET /api/v1/drivers`, `GET /api/v1/trips`, `/ws` |

The WebSocket is opened **once** in `Layout` and shared through React
context, so switching pages doesn't drop and reopen the connection.

## Design decisions

- Warm charcoal background, taxi-medallion yellow as the main accent;
  teal and rust are reserved for status (available/dispatched vs
  busy/error) and mean the same thing on every page.
- Big Shoulders Display for headings and big numbers, IBM Plex Sans for
  text, IBM Plex Mono only for tabular data (IDs, coordinates, times).
- Motion happens only when something actually happened (a new trip row
  flashes once); `prefers-reduced-motion` is respected; keyboard focus is
  always visible; the layout collapses to a top bar under 860px.

## What the dashboard does NOT show (and why)

The original spec listed requests/sec, average latency, p95 latency,
prediction latency and API error count. **The backend doesn't expose
those yet** — they belong to Phase 8 (Prometheus metrics). Rather than
invent numbers, the dashboard shows only what the API really returns:
queue length/capacity, busy workers, cache hits/misses/hit rate. Two
counters ("trips created", "predictions served") are counted from
WebSocket events since the page was opened, and are labeled
"this session" for that reason. Likewise the trips table has no ETA or
prediction-latency column, because the `Trip` record doesn't store them
today. Both gaps close naturally in Phase 8.

## Verified

- `npm run build` → succeeds (one advisory: the JS bundle is ~795 kB
  because of Recharts + Leaflet; fine for a dev console, splittable later
  with dynamic `import()`).
- `npm run lint` (oxlint) → 0 warnings, 0 errors. The linter caught a real
  issue in the first version of `useLiveEvents` (recursive reconnect, and
  a reconnect timer never cleared on unmount); it was rewritten so the
  socket and timer are both torn down when the component unmounts.
- CORS preflight against the running Go server: `OPTIONS /api/v1/predict`
  with `Origin: http://localhost:5173` returns `204` with
  `Access-Control-Allow-Origin/Methods/Headers`, so the browser can POST.

**Not verified here:** how it looks in a real browser. This sandbox has
no browser, so nobody has seen the rendered pages yet — layout, map tiles
and chart appearance are unchecked. Please look at each page and tell me
what needs adjusting.

## How to run

```powershell
cd frontend
npm install
npm run dev
```
Open http://localhost:5173. The backend (Go + ml-service + Redis) must be
running as in earlier phases. To point at a different backend:
```powershell
copy .env.example .env    # then edit VITE_API_BASE_URL
```

Good things to try, in this order:
1. **Predict a trip** → submit the same form twice; the second shows "Cache: hit".
2. **Dashboard** → keep it open, then run the simulator in another terminal:
   `go run ./simulator/cmd/simulator --file ..\ml-service\data\train.csv --speed 50 --limit 30 --target http://localhost:8080`
   and watch the events and counters move.
3. **Trips** → new rows flash as the simulator sends them.
4. **Map** → drivers turn from teal to rust as they get dispatched.

Note: the backend only seeds 20 drivers, so after ~20 trips the rest stay
`pending` (see Phase 6). Restart the Go server to reset drivers and trips.

## Next: Phase 8 — Observability

Prometheus metrics (`http_requests_total`, latency histograms,
`queue_length`, `cache_hits_total`, …), a `/metrics` endpoint, and a
Grafana dashboard — which also supplies the requests/sec, p95 and error
numbers the dashboard is currently missing.
