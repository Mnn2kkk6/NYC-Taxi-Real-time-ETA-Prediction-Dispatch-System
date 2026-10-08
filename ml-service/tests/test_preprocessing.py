import pandas as pd
import pytest

from app.preprocessing import clean


def make_row(**overrides):
    base = dict(
        id="id1",
        vendor_id=1,
        pickup_datetime=pd.Timestamp("2016-03-14 17:24:55"),
        dropoff_datetime=pd.Timestamp("2016-03-14 17:32:30"),
        passenger_count=1,
        pickup_longitude=-73.98,
        pickup_latitude=40.76,
        dropoff_longitude=-73.96,
        dropoff_latitude=40.75,
        store_and_fwd_flag="N",
        trip_duration=455,
    )
    base.update(overrides)
    return base


def test_valid_row_survives():
    df = pd.DataFrame([make_row()])
    cleaned, report = clean(df, is_train=True)
    assert len(cleaned) == 1


def test_zero_passenger_count_removed():
    df = pd.DataFrame([make_row(passenger_count=0)])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 0


def test_too_many_passengers_removed():
    df = pd.DataFrame([make_row(passenger_count=9)])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 0


def test_out_of_nyc_bbox_removed():
    df = pd.DataFrame([make_row(dropoff_latitude=32.18)])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 0


def test_trip_too_short_removed():
    df = pd.DataFrame([make_row(trip_duration=5)])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 0


def test_trip_too_long_removed():
    df = pd.DataFrame([make_row(trip_duration=3_526_282)])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 0


def test_duplicate_ids_deduplicated():
    df = pd.DataFrame([make_row(id="dup"), make_row(id="dup")])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 1


def test_zero_distance_long_duration_removed():
    # pickup == dropoff but duration is 20 minutes -> stationary meter artifact
    df = pd.DataFrame([make_row(
        pickup_latitude=40.76, pickup_longitude=-73.98,
        dropoff_latitude=40.76, dropoff_longitude=-73.98,
        trip_duration=1200,
    )])
    cleaned, _ = clean(df, is_train=True)
    assert len(cleaned) == 0


def test_test_data_skips_duration_filters():
    # test-mode rows have no trip_duration column at all
    row = make_row()
    del row["trip_duration"]
    del row["dropoff_datetime"]
    df = pd.DataFrame([row])
    cleaned, _ = clean(df, is_train=False)
    assert len(cleaned) == 1
