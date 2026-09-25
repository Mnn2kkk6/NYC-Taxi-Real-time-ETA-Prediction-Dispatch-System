# Phase 2 — Python ML API (FastAPI) — DONE

Wraps the Phase 1 XGBoost model in a FastAPI service. Model is loaded
**once** at startup (`lifespan` hook in `main.py`), never per request.

## Files

```
ml-service/
├── app/
│   ├── schemas.py        # Pydantic request/response models, validation mirrors Phase 1 filters
│   ├── model_service.py  # loads model once, computes features, predicts
│   └── main.py            # FastAPI app: /health, /predict, /model/info
├── tests/
│   └── test_api.py        # 8 tests using FastAPI TestClient
└── Dockerfile
```

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Returns `{"status":"ok","model_loaded":true}` |
| GET | `/model/info` | Model version, feature list, training/validation row counts, full benchmark results |
| POST | `/predict` | Takes raw trip request, returns predicted duration + distance |

### `/predict` request/response (real example, actually run)

Request:
```json
{
  "pickup_latitude": 40.761,
  "pickup_longitude": -73.982,
  "dropoff_latitude": 40.730,
  "dropoff_longitude": -73.995,
  "passenger_count": 2,
  "pickup_datetime": "2016-03-15T18:30:00"
}
```

Response (actual output from a real run):
```json
{
  "predicted_duration_seconds": 1076,
  "predicted_duration_minutes": 17.9,
  "distance_km": 3.62,
  "model_version": "xgboost-v1",
  "request_id": "534e886c-31ea-4e92-a3ef-54259b3dd308",
  "prediction_timestamp": "2026-09-24T09:41:29.155331Z"
}
```

## Validation (rejects bad input with 422, before it ever reaches the model)

Mirrors the Phase 1 cleaning filters exactly, imported directly from
`app/preprocessing.py` so the two can never drift apart:

- `pickup_latitude`/`pickup_longitude`/`dropoff_latitude`/`dropoff_longitude` must be inside the NYC bounding box (lat 40.5–40.9, lon -74.05 to -73.70)
- `passenger_count` must be 1–6
- `store_and_fwd_flag` must be `Y` or `N`
- `pickup_datetime` is required

## Architecture decision: feature engineering lives here, not in Go

The Go backend (Phase 3) forwards the **raw** trip fields to this service
as-is; `model_service.py` computes all 14 features (`haversine_km`,
`pickup_hour`, `is_rush_hour`, etc.) internally, using the exact same
`app/features.py` module trained on in Phase 1. This was a deliberate
choice over duplicating feature logic in Go: one source of truth for
feature engineering, zero risk of train/serve skew between two languages.
Go's job is transport, validation, orchestration, concurrency — not
re-implementing feature math.

## Test results (real, `pytest -v`)

```
23 passed in 1.73s
```
(15 from Phase 1 preprocessing/features + 8 new API tests: health, model
info, valid prediction, out-of-bounds coords rejected, invalid passenger
count rejected, bad flag rejected, missing field rejected, zero-distance
edge case)

## How to run

```bash
cd ml-service
pip install -r requirements.txt
python -m pytest tests/ -v
uvicorn app.main:app --reload
```

Then open `http://127.0.0.1:8000/docs` for interactive Swagger UI.

## Docker

```bash
cd ml-service
docker build -t nyc-taxi-ml-service .
docker run -p 8000:8000 nyc-taxi-ml-service
```

Non-root user, healthcheck hitting `/health` every 30s, multi-stage not
needed here (Python has no compile step) but the image only copies
`app/` and `models/` — no dataset, no notebooks, no test files.

## Next: Phase 3 — Go Backend

Done — see `PHASE3_README.md`.
