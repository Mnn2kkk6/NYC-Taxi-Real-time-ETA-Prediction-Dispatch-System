"""
model_service.py

Loads the trained XGBoost model ONCE (module-level singleton) and exposes
a predict() method. FastAPI's startup event calls load() a single time;
every request reuses the same in-memory model — never reloaded per request.
"""

import json
import logging
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path

import numpy as np
import pandas as pd
from xgboost import XGBRegressor

from app.features import FEATURE_COLUMNS, build_features, get_feature_matrix
from app.schemas import PredictionRequest, PredictionResponse

logger = logging.getLogger(__name__)


class ModelService:
    """Singleton-style wrapper around the trained XGBoost model."""

    def __init__(self, model_path: str, metadata_path: str):
        self.model_path = Path(model_path)
        self.metadata_path = Path(metadata_path)
        self._model: XGBRegressor | None = None
        self._metadata: dict | None = None

    def load(self) -> None:
        if not self.model_path.exists():
            raise FileNotFoundError(
                f"Model file not found at {self.model_path}. Run `python -m app.train` first (Phase 1)."
            )
        model = XGBRegressor()
        model.load_model(str(self.model_path))
        self._model = model
        self._metadata = json.loads(self.metadata_path.read_text())
        logger.info("Model loaded from %s (version=%s)", self.model_path, self._metadata.get("model_version"))

    @property
    def is_loaded(self) -> bool:
        return self._model is not None

    @property
    def metadata(self) -> dict:
        if self._metadata is None:
            raise RuntimeError("Model not loaded yet")
        return self._metadata

    def predict(self, request: PredictionRequest) -> PredictionResponse:
        if self._model is None:
            raise RuntimeError("Model not loaded — call load() at startup before serving requests")

        row = pd.DataFrame([{
            "vendor_id": request.vendor_id,
            "pickup_datetime": request.pickup_datetime,
            "passenger_count": request.passenger_count,
            "pickup_longitude": request.pickup_longitude,
            "pickup_latitude": request.pickup_latitude,
            "dropoff_longitude": request.dropoff_longitude,
            "dropoff_latitude": request.dropoff_latitude,
            "store_and_fwd_flag": request.store_and_fwd_flag,
        }])

        featured = build_features(row)
        distance_km = float(featured["haversine_km"].iloc[0])
        X = featured[FEATURE_COLUMNS]

        pred_log = self._model.predict(X)[0]
        duration_sec = max(1, round(float(np.expm1(pred_log))))

        return PredictionResponse(
            predicted_duration_seconds=duration_sec,
            predicted_duration_minutes=round(duration_sec / 60.0, 1),
            distance_km=round(distance_km, 2),
            model_version=self._metadata.get("model_version", "unknown"),
            request_id=str(uuid.uuid4()),
            prediction_timestamp=datetime.now(timezone.utc),
        )


# module-level singleton, initialized by FastAPI's startup event in main.py
model_service = ModelService(
    model_path="models/xgboost_model.json",
    metadata_path="models/model_metadata.json",
)