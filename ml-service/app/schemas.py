"""
schemas.py

Pydantic models for the /predict API. Validation here mirrors the
cleaning rules from Phase 1 (preprocessing.py) so a live request gets
rejected with a clear 422 instead of silently producing a garbage
prediction from out-of-distribution input.
"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field, field_validator

from app.preprocessing import (
    MAX_PASSENGER_COUNT,
    MIN_PASSENGER_COUNT,
    NYC_LAT_MAX,
    NYC_LAT_MIN,
    NYC_LON_MAX,
    NYC_LON_MIN,
)


class PredictionRequest(BaseModel):
    pickup_latitude: float = Field(..., ge=NYC_LAT_MIN, le=NYC_LAT_MAX)
    pickup_longitude: float = Field(..., ge=NYC_LON_MIN, le=NYC_LON_MAX)
    dropoff_latitude: float = Field(..., ge=NYC_LAT_MIN, le=NYC_LAT_MAX)
    dropoff_longitude: float = Field(..., ge=NYC_LON_MIN, le=NYC_LON_MAX)
    passenger_count: int = Field(..., ge=MIN_PASSENGER_COUNT, le=MAX_PASSENGER_COUNT)
    pickup_datetime: datetime
    vendor_id: int = Field(default=1, ge=1, le=2)
    store_and_fwd_flag: str = Field(default="N")

    @field_validator("store_and_fwd_flag")
    @classmethod
    def flag_must_be_y_or_n(cls, v: str) -> str:
        v = v.upper()
        if v not in ("Y", "N"):
            raise ValueError("store_and_fwd_flag must be 'Y' or 'N'")
        return v

    model_config = ConfigDict(
        json_schema_extra={
            "example": {
                "pickup_latitude": 40.761,
                "pickup_longitude": -73.982,
                "dropoff_latitude": 40.730,
                "dropoff_longitude": -73.995,
                "passenger_count": 2,
                "pickup_datetime": "2016-03-15T18:30:00",
            }
        }
    )


class PredictionResponse(BaseModel):
    predicted_duration_seconds: int
    predicted_duration_minutes: float
    distance_km: float
    model_version: str
    request_id: str
    prediction_timestamp: datetime


class HealthResponse(BaseModel):
    status: str
    model_loaded: bool


class ModelInfoResponse(BaseModel):
    model_version: str
    feature_columns: list[str]
    training_rows: int
    validation_rows: int
    model_size_kb: float
    benchmark_results: list[dict]