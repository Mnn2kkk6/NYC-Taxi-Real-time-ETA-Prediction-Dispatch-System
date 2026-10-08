import pandas as pd
import pytest

from app.features import FEATURE_COLUMNS, build_features, get_feature_matrix, haversine_km, manhattan_km


def make_df():
    return pd.DataFrame([{
        "vendor_id": 2,
        "pickup_datetime": pd.Timestamp("2016-03-14 17:24:55"),  # Monday, rush hour
        "passenger_count": 2,
        "pickup_longitude": -73.982154846191406,
        "pickup_latitude": 40.767936706542969,
        "dropoff_longitude": -73.964630126953125,
        "dropoff_latitude": 40.765602111816406,
        "store_and_fwd_flag": "N",
    }])


def test_haversine_known_short_distance():
    # ~1.5km trip from the real dataset sample row
    d = haversine_km(40.767936706542969, -73.982154846191406, 40.765602111816406, -73.964630126953125)
    assert 1.0 < d.iloc[0] < 2.0 if hasattr(d, "iloc") else 1.0 < d < 2.0


def test_manhattan_gte_haversine():
    df = make_df()
    feats = build_features(df)
    assert (feats["manhattan_km"] >= feats["haversine_km"]).all()


def test_rush_hour_flag_correct():
    df = make_df()  # 17:24 -> rush hour
    feats = build_features(df)
    assert feats["is_rush_hour"].iloc[0] == 1
    assert feats["is_night"].iloc[0] == 0


def test_weekend_flag():
    df = make_df()
    df["pickup_datetime"] = pd.Timestamp("2016-03-19 12:00:00")  # Saturday
    feats = build_features(df)
    assert feats["is_weekend"].iloc[0] == 1


def test_no_leakage_columns_in_feature_matrix():
    df = make_df()
    matrix = get_feature_matrix(df)
    forbidden = {"trip_duration", "dropoff_datetime", "actual_duration", "status"}
    assert forbidden.isdisjoint(set(matrix.columns))
    assert list(matrix.columns) == FEATURE_COLUMNS


def test_store_and_fwd_flag_encoded_binary():
    df = make_df()
    df["store_and_fwd_flag"] = "Y"
    feats = build_features(df)
    assert feats["store_and_fwd_flag"].iloc[0] == 1
