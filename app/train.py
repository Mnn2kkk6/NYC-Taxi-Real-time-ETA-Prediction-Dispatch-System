"""
train.py

End-to-end training entrypoint:
    raw CSV -> clean -> feature engineer -> split -> train baseline + XGBoost
    -> evaluate -> save best model + metadata

Run:
    cd ml-service
    python -m app.train --data ../data/train.csv --out models/

Target: we train on log1p(trip_duration) because the raw target is heavily
right-skewed (see EDA notes below) and log-space training is what the
original Kaggle competition scored on (RMSLE). We report metrics in BOTH
log space and back-transformed seconds so they're interpretable.
"""

import argparse
import json
import logging
import time
from pathlib import Path

import joblib
import numpy as np
import pandas as pd
from sklearn.ensemble import RandomForestRegressor
from sklearn.linear_model import LinearRegression
from sklearn.metrics import mean_absolute_error, mean_squared_error, r2_score
from sklearn.model_selection import train_test_split
from xgboost import XGBRegressor

from app.features import FEATURE_COLUMNS, get_feature_matrix
from app.preprocessing import clean, load_raw

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
logger = logging.getLogger(__name__)


def evaluate(y_true_log, y_pred_log, label: str) -> dict:
    y_true_sec = np.expm1(y_true_log)
    y_pred_sec = np.expm1(y_pred_log)
    y_pred_sec = np.clip(y_pred_sec, 1, None)  # duration can't be negative/zero

    metrics = {
        "model": label,
        "mae_log_seconds": round(mean_absolute_error(y_true_log, y_pred_log), 4),
        "rmse_log_seconds": round(np.sqrt(mean_squared_error(y_true_log, y_pred_log)), 4),
        "r2_log_seconds": round(r2_score(y_true_log, y_pred_log), 4),
        "mae_seconds": round(mean_absolute_error(y_true_sec, y_pred_sec), 2),
        "rmse_seconds": round(np.sqrt(mean_squared_error(y_true_sec, y_pred_sec)), 2),
        "r2_seconds": round(r2_score(y_true_sec, y_pred_sec), 4),
    }
    return metrics


def main(data_path: str, out_dir: str, sample_frac: float | None = None):
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)

    logger.info("Loading raw data from %s", data_path)
    df_raw = load_raw(data_path)
    logger.info("Raw shape: %s", df_raw.shape)

    if sample_frac:
        df_raw = df_raw.sample(frac=sample_frac, random_state=42).reset_index(drop=True)
        logger.info("Sampled down to %s rows (frac=%s)", len(df_raw), sample_frac)

    df_clean, report = clean(df_raw, is_train=True)
    logger.info("Clean shape: %s (%.2f%% of raw retained)", df_clean.shape, 100 * len(df_clean) / len(df_raw))
    report_path = out / "cleaning_report.csv"
    report.as_dataframe().to_csv(report_path, index=False)
    logger.info("Cleaning report saved to %s", report_path)

    X = get_feature_matrix(df_clean)
    y_log = np.log1p(df_clean["trip_duration"].values)

    X_train, X_val, y_train, y_val = train_test_split(X, y_log, test_size=0.15, random_state=42)
    logger.info("Train: %s rows | Val: %s rows", len(X_train), len(X_val))

    results = []

    # --- Baseline 1: Linear Regression ---
    t0 = time.time()
    lr = LinearRegression()
    lr.fit(X_train, y_train)
    lr_train_time = time.time() - t0
    lr_pred = lr.predict(X_val)
    lr_metrics = evaluate(y_val, lr_pred, "linear_regression")
    lr_metrics["train_time_sec"] = round(lr_train_time, 2)
    results.append(lr_metrics)
    logger.info("LinearRegression: %s", lr_metrics)

    # --- Baseline 2: Random Forest (smaller, it's just a benchmark) ---
    # max_samples subsamples each tree's bootstrap for tractable training time
    # on 1.2M rows single-core; this is a baseline for comparison, not production.
    t0 = time.time()
    rf = RandomForestRegressor(n_estimators=30, max_depth=10, max_samples=0.3, n_jobs=-1, random_state=42)
    rf.fit(X_train, y_train)
    rf_train_time = time.time() - t0
    rf_pred = rf.predict(X_val)
    rf_metrics = evaluate(y_val, rf_pred, "random_forest")
    rf_metrics["train_time_sec"] = round(rf_train_time, 2)
    results.append(rf_metrics)
    logger.info("RandomForest: %s", rf_metrics)

    # --- Main model: XGBoost ---
    t0 = time.time()
    xgb = XGBRegressor(
        n_estimators=400,
        max_depth=8,
        learning_rate=0.05,
        subsample=0.8,
        colsample_bytree=0.8,
        tree_method="hist",
        n_jobs=-1,
        random_state=42,
        eval_metric="rmse",
        early_stopping_rounds=30,
    )
    xgb.fit(X_train, y_train, eval_set=[(X_val, y_val)], verbose=False)
    xgb_train_time = time.time() - t0

    t0 = time.time()
    xgb_pred = xgb.predict(X_val)
    xgb_inference_time_ms = (time.time() - t0) * 1000 / len(X_val)
    xgb_metrics = evaluate(y_val, xgb_pred, "xgboost")
    xgb_metrics["train_time_sec"] = round(xgb_train_time, 2)
    xgb_metrics["avg_inference_ms_per_row"] = round(xgb_inference_time_ms, 4)
    xgb_metrics["best_iteration"] = int(xgb.best_iteration) if xgb.best_iteration else xgb.n_estimators
    results.append(xgb_metrics)
    logger.info("XGBoost: %s", xgb_metrics)

    # --- feature importance ---
    importance = dict(zip(FEATURE_COLUMNS, xgb.feature_importances_.tolist()))
    importance = dict(sorted(importance.items(), key=lambda kv: kv[1], reverse=True))

    # --- save model (production choice: XGBoost — best accuracy AND fast enough inference) ---
    model_path = out / "xgboost_model.json"
    xgb.save_model(str(model_path))

    model_size_kb = model_path.stat().st_size / 1024

    metadata = {
        "model_version": "xgboost-v1",
        "feature_columns": FEATURE_COLUMNS,
        "target_transform": "log1p(trip_duration_seconds)",
        "model_size_kb": round(model_size_kb, 1),
        "training_rows": len(X_train),
        "validation_rows": len(X_val),
        "feature_importance": importance,
        "benchmark_results": results,
    }
    meta_path = out / "model_metadata.json"
    meta_path.write_text(json.dumps(metadata, indent=2))

    # also persist the baseline models for the "why we picked XGBoost" comparison story
    joblib.dump(lr, out / "baseline_linear_regression.joblib")
    joblib.dump(rf, out / "baseline_random_forest.joblib")

    logger.info("Saved XGBoost model to %s (%.1f KB)", model_path, model_size_kb)
    logger.info("Saved metadata to %s", meta_path)
    logger.info("=== BENCHMARK SUMMARY ===")
    for r in results:
        logger.info(r)

    return metadata


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--data", type=str, default="../data/train.csv")
    parser.add_argument("--out", type=str, default="models")
    parser.add_argument("--sample-frac", type=float, default=None, help="Optional: train on a subsample for fast iteration")
    args = parser.parse_args()
    main(args.data, args.out, args.sample_frac)
