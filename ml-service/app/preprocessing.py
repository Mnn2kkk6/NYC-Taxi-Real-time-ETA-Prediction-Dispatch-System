"""
preprocessing.py

Cleans the raw NYC Taxi Trip Duration dataset.

Every filter below is documented with WHY it exists and its measured impact,
because this needs to be defensible in a README / interview, not arbitrary.

NYC bounding box used: lat in [40.5, 40.9], lon in [-74.05, -73.70].
This covers all 5 boroughs plus JFK/LGA/Newark-adjacent airspace, while
dropping GPS points that are clearly wrong (e.g. dropoff_latitude=32.18,
which is nowhere near New York).
"""

import logging
from dataclasses import dataclass, field

import pandas as pd

logger = logging.getLogger(__name__)

NYC_LAT_MIN, NYC_LAT_MAX = 40.5, 40.9
NYC_LON_MIN, NYC_LON_MAX = -74.05, -73.70

MIN_TRIP_DURATION_SEC = 30       # below this: almost certainly a cancelled/glitched trip
MAX_TRIP_DURATION_SEC = 6 * 3600  # above this: GPS/meter error, not a real taxi ride
MIN_PASSENGER_COUNT = 1
MAX_PASSENGER_COUNT = 6           # NYC legal taxi capacity; 7-9 are documented data errors


@dataclass
class FilterReport:
    """Tracks how many rows each filter removes, so nothing is silently dropped."""
    steps: list = field(default_factory=list)

    def log_step(self, name: str, before: int, after: int):
        removed = before - after
        pct = (removed / before * 100) if before else 0.0
        self.steps.append(
            {"filter": name, "before": before, "after": after, "removed": removed, "pct_removed": round(pct, 3)}
        )
        logger.info("[filter] %s: %d -> %d (removed %d, %.3f%%)", name, before, after, removed, pct)

    def as_dataframe(self) -> pd.DataFrame:
        return pd.DataFrame(self.steps)


def load_raw(path: str) -> pd.DataFrame:
    df = pd.read_csv(path)
    df["pickup_datetime"] = pd.to_datetime(df["pickup_datetime"])
    if "dropoff_datetime" in df.columns:
        df["dropoff_datetime"] = pd.to_datetime(df["dropoff_datetime"])
    return df


def clean(df: pd.DataFrame, is_train: bool = True) -> tuple[pd.DataFrame, FilterReport]:
    """
    Applies documented cleaning filters. For test data (is_train=False),
    duration-based filters are skipped since test has no trip_duration/
    dropoff_datetime (that's the whole point: predicting it at request time).
    """
    report = FilterReport()
    df = df.drop_duplicates(subset="id").reset_index(drop=True)
    report.log_step("drop_duplicate_ids", len(df) + df.duplicated(subset="id").sum(), len(df))

    n0 = len(df)

    # --- passenger count ---
    df = df[(df["passenger_count"] >= MIN_PASSENGER_COUNT) & (df["passenger_count"] <= MAX_PASSENGER_COUNT)]
    report.log_step("passenger_count_1_to_6", n0, len(df))
    n0 = len(df)

    # --- geographic bounding box (both pickup and dropoff) ---
    df = df[
        df["pickup_latitude"].between(NYC_LAT_MIN, NYC_LAT_MAX)
        & df["pickup_longitude"].between(NYC_LON_MIN, NYC_LON_MAX)
        & df["dropoff_latitude"].between(NYC_LAT_MIN, NYC_LAT_MAX)
        & df["dropoff_longitude"].between(NYC_LON_MIN, NYC_LON_MAX)
    ]
    report.log_step("nyc_bounding_box", n0, len(df))
    n0 = len(df)

    if is_train and "trip_duration" in df.columns:
        df = df[df["trip_duration"].between(MIN_TRIP_DURATION_SEC, MAX_TRIP_DURATION_SEC)]
        report.log_step("trip_duration_30s_to_6h", n0, len(df))
        n0 = len(df)

        # zero-distance trips with non-trivial duration are almost always
        # a stationary meter (e.g. driver waiting) mislabeled as a "trip" —
        # drop only the extreme cases (duration > 5 min while distance == 0)
        from app.features import haversine_km  # local import to avoid circularity at module load

        dist = haversine_km(
            df["pickup_latitude"], df["pickup_longitude"], df["dropoff_latitude"], df["dropoff_longitude"]
        )
        bad_zero_distance = (dist < 0.05) & (df["trip_duration"] > 300)
        df = df[~bad_zero_distance]
        report.log_step("zero_distance_long_duration", n0, len(df))

    return df.reset_index(drop=True), report
