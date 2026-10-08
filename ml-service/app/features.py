"""
features.py

Feature engineering for trip-duration prediction.

HARD RULE: every feature here must be computable at the moment a trip is
REQUESTED (pickup coordinates, dropoff coordinates, passenger count,
pickup datetime). Nothing derived from dropoff_datetime, actual duration,
or trip status is allowed in here — that would be leakage, since none of
that exists yet when a real user asks for an ETA.
"""

import numpy as np
import pandas as pd

EARTH_RADIUS_KM = 6371.0

FEATURE_COLUMNS = [
    "passenger_count",
    "vendor_id",
    "store_and_fwd_flag",
    "haversine_km",
    "manhattan_km",
    "pickup_hour",
    "pickup_day_of_week",
    "pickup_month",
    "is_weekend",
    "is_rush_hour",
    "is_night",
    "is_morning",
    "is_afternoon",
    "is_evening",
]


def haversine_km(lat1, lon1, lat2, lon2):
    lat1, lon1, lat2, lon2 = map(np.radians, [lat1, lon1, lat2, lon2])
    dlat = lat2 - lat1
    dlon = lon2 - lon1
    a = np.sin(dlat / 2.0) ** 2 + np.cos(lat1) * np.cos(lat2) * np.sin(dlon / 2.0) ** 2
    c = 2 * np.arcsin(np.sqrt(a))
    return EARTH_RADIUS_KM * c


def manhattan_km(lat1, lon1, lat2, lon2):
    """Approximates NYC street-grid distance better than pure haversine."""
    a = haversine_km(lat1, lon1, lat1, lon2)
    b = haversine_km(lat1, lon1, lat2, lon1)
    return a + b


def build_features(df: pd.DataFrame) -> pd.DataFrame:
    df = df.copy()

    df["haversine_km"] = haversine_km(
        df["pickup_latitude"], df["pickup_longitude"], df["dropoff_latitude"], df["dropoff_longitude"]
    )
    df["manhattan_km"] = manhattan_km(
        df["pickup_latitude"], df["pickup_longitude"], df["dropoff_latitude"], df["dropoff_longitude"]
    )

    dt = df["pickup_datetime"]
    df["pickup_hour"] = dt.dt.hour
    df["pickup_day_of_week"] = dt.dt.dayofweek  # 0=Monday
    df["pickup_month"] = dt.dt.month
    df["is_weekend"] = (df["pickup_day_of_week"] >= 5).astype(int)
    df["is_rush_hour"] = df["pickup_hour"].isin([7, 8, 9, 16, 17, 18]).astype(int)
    df["is_night"] = df["pickup_hour"].isin([23, 0, 1, 2, 3, 4, 5]).astype(int)
    df["is_morning"] = df["pickup_hour"].between(6, 11).astype(int)
    df["is_afternoon"] = df["pickup_hour"].between(12, 16).astype(int)
    df["is_evening"] = df["pickup_hour"].between(17, 22).astype(int)

    df["store_and_fwd_flag"] = (df["store_and_fwd_flag"] == "Y").astype(int)
    df["vendor_id"] = df["vendor_id"].astype(int)
    df["passenger_count"] = df["passenger_count"].astype(int)

    return df


def get_feature_matrix(df: pd.DataFrame) -> pd.DataFrame:
    df = build_features(df)
    return df[FEATURE_COLUMNS]
