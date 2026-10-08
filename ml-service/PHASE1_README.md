# Phase 1 — Dataset + ML (DONE)

Trained and evaluated on your actual `train.csv` (1,458,644 rows). All numbers
below are real output from `app/train.py`, not estimated.

## How to run it yourself

```bash
cd ml-service
pip install -r requirements.txt
python -m pytest tests/ -v                       # 15 tests, all passing
python -m app.train --data ../data/train.csv --out models/
```

`--sample-frac 0.1` trains on a 10% subsample for fast iteration while developing.

## Data cleaning (documented filters, nothing arbitrary)

| Filter | Rows removed | % of raw | Reasoning |
|---|---|---|---|
| Duplicate `id` | 0 | 0.000% | None found in this dataset |
| `passenger_count` outside 1–6 | 65 | 0.004% | 0 passengers is invalid; NYC taxi legal capacity is 6; 7–9 (5 rows total) are data errors |
| Outside NYC bounding box (lat 40.5–40.9, lon -74.05 to -73.70) | 5,274 | 0.362% | GPS points nowhere near NYC (e.g. one dropoff at lat 32.18) |
| `trip_duration` outside 30s–6h | 6,575 | 0.452% | Sub-30s trips are cancellations/glitches; the dataset's max (3,526,282s ≈ 40 days) is clearly a meter error |
| Zero-distance trips lasting >5min | 4,114 | 0.284% | Stationary-meter artifacts — same pickup/dropoff coords but long duration |

**Total retained: 1,442,616 / 1,458,644 rows (98.90%)** — full breakdown in `models/cleaning_report.csv`.

## Feature engineering (leakage-safe)

Every feature is computable at the moment a trip is **requested** — nothing
derived from `dropoff_datetime` or `trip_duration` leaks into `X`. Confirmed
by `test_no_leakage_columns_in_feature_matrix`.

Geographic: `haversine_km`, `manhattan_km` (grid-distance approximation, better fit for NYC streets)
Temporal: `pickup_hour`, `pickup_day_of_week`, `pickup_month`, `is_weekend`, `is_rush_hour`, `is_night`, `is_morning`, `is_afternoon`, `is_evening`
Trip: `passenger_count`, `vendor_id`, `store_and_fwd_flag`

Target is trained as `log1p(trip_duration)` (the raw target is heavily
right-skewed — this matches how the original Kaggle competition scored
RMSLE). Metrics below are reported in both log-space and back-transformed
seconds.

## Benchmark results (real, from this run — 1,226,223 train / 216,393 val rows)

| Model | MAE (sec) | RMSE (sec) | R² (sec) | R² (log) | Train time | Notes |
|---|---|---|---|---|---|---|
| Linear Regression | 346.99 | 715.10 | -0.192 | 0.474 | 0.48s | Negative R² in raw-second space — linear model badly underfits long trips |
| Random Forest (30 trees, depth 10, 30% bootstrap subsample) | 222.12 | 357.63 | 0.702 | 0.710 | 40.6s | Reasonable, but XGBoost beats it on every metric |
| **XGBoost (400 est., depth 8, early-stopped at 368)** | **217.23** | **349.96** | **0.715** | **0.718** | 44.9s | **Chosen for production** |

**Why XGBoost, not just "highest accuracy":**
- Best MAE/RMSE/R² of the three
- Inference: **0.0065 ms/row** — irrelevant to the concurrency story we're building; the Go worker pool's latency budget won't be spent on the model
- Model size: **10.4 MB** — fits fine as a Docker image layer, no Git LFS needed
- Deployment: XGBoost has a native, dependency-light `save_model`/`load_model` (JSON format) — no pickling version-compatibility risk when the FastAPI service loads it later

## Feature importance (from the trained model)

1. `haversine_km` — 63.7%
2. `manhattan_km` — 13.0%
3. `is_night` — 5.2%
4. `is_weekend` — 4.5%
5. `pickup_hour` — 4.2%

Distance dominates, as expected. Time-of-day/day-of-week features contribute
a meaningful ~15% combined — this is the "why not just distance / speed"
story for the interview: NYC traffic patterns genuinely shift ETA.

## Files produced

```
ml-service/
├── app/
│   ├── preprocessing.py     # documented cleaning filters
│   ├── features.py          # leakage-safe feature engineering
│   └── train.py             # training + benchmark + save
├── tests/
│   ├── test_preprocessing.py  # 9 tests
│   └── test_features.py       # 6 tests
├── models/
│   ├── xgboost_model.json         # production model
│   ├── baseline_linear_regression.joblib
│   ├── baseline_random_forest.joblib
│   ├── model_metadata.json        # feature list, importance, full benchmark
│   └── cleaning_report.csv        # row-by-row filter audit trail
└── requirements.txt
```

## Next: Phase 2 — Python ML API (FastAPI)

`POST /predict`, `GET /health`, `GET /model/info` — load `xgboost_model.json`
once at startup, validate input, compute the same features as above, return
prediction + distance + model_version. Ready to build once you confirm this
phase looks right.
