import pytest
from fastapi.testclient import TestClient

from app.main import app

VALID_PAYLOAD = {
    "pickup_latitude": 40.761,
    "pickup_longitude": -73.982,
    "dropoff_latitude": 40.730,
    "dropoff_longitude": -73.995,
    "passenger_count": 2,
    "pickup_datetime": "2016-03-15T18:30:00",
}


@pytest.fixture(scope="module")
def client():
    # TestClient's context manager triggers the lifespan startup (model load)
    with TestClient(app) as c:
        yield c


def test_health_reports_model_loaded(client):
    resp = client.get("/health")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert body["model_loaded"] is True


def test_model_info_returns_metadata(client):
    resp = client.get("/model/info")
    assert resp.status_code == 200
    body = resp.json()
    assert body["model_version"] == "xgboost-v1"
    assert "haversine_km" in body["feature_columns"]
    assert body["training_rows"] > 0


def test_predict_valid_request(client):
    resp = client.post("/predict", json=VALID_PAYLOAD)
    assert resp.status_code == 200
    body = resp.json()
    assert body["predicted_duration_seconds"] > 0
    assert body["predicted_duration_minutes"] > 0
    assert body["distance_km"] > 0
    assert body["model_version"] == "xgboost-v1"
    assert "request_id" in body


def test_predict_rejects_out_of_nyc_coords(client):
    payload = dict(VALID_PAYLOAD, pickup_latitude=32.18)  # the known bad row from the raw dataset
    resp = client.post("/predict", json=payload)
    assert resp.status_code == 422


def test_predict_rejects_invalid_passenger_count(client):
    payload = dict(VALID_PAYLOAD, passenger_count=0)
    resp = client.post("/predict", json=payload)
    assert resp.status_code == 422

    payload = dict(VALID_PAYLOAD, passenger_count=9)
    resp = client.post("/predict", json=payload)
    assert resp.status_code == 422


def test_predict_rejects_bad_store_and_fwd_flag(client):
    payload = dict(VALID_PAYLOAD, store_and_fwd_flag="X")
    resp = client.post("/predict", json=payload)
    assert resp.status_code == 422


def test_predict_missing_required_field(client):
    payload = dict(VALID_PAYLOAD)
    del payload["pickup_datetime"]
    resp = client.post("/predict", json=payload)
    assert resp.status_code == 422


def test_predict_same_pickup_dropoff_gives_small_distance(client):
    payload = dict(VALID_PAYLOAD, dropoff_latitude=VALID_PAYLOAD["pickup_latitude"],
                    dropoff_longitude=VALID_PAYLOAD["pickup_longitude"])
    resp = client.post("/predict", json=payload)
    assert resp.status_code == 200
    assert resp.json()["distance_km"] < 0.01