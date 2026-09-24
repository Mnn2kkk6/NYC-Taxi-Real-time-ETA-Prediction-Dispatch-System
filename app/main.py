"""
main.py

FastAPI ML inference service.

Run:
    cd ml-service
    uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload

The model is loaded exactly ONCE, in the lifespan startup hook — not per
request. This is the service the Go backend's mlclient will call.
"""

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware

from app.model_service import model_service
from app.schemas import HealthResponse, ModelInfoResponse, PredictionRequest, PredictionResponse

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    logger.info("Starting up: loading model...")
    model_service.load()
    logger.info("Model loaded. Service ready.")
    yield
    logger.info("Shutting down ML service.")


app = FastAPI(
    title="NYC Taxi ETA Prediction Service",
    version="1.0.0",
    lifespan=lifespan,
)

# CORS: the Go backend and/or browser dashboard call this service.
# Tighten allow_origins in production via env var instead of "*".
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["GET", "POST"],
    allow_headers=["*"],
)


@app.get("/health", response_model=HealthResponse)
def health():
    return HealthResponse(status="ok", model_loaded=model_service.is_loaded)


@app.get("/model/info", response_model=ModelInfoResponse)
def model_info():
    if not model_service.is_loaded:
        raise HTTPException(status_code=503, detail="Model not loaded")
    meta = model_service.metadata
    return ModelInfoResponse(
        model_version=meta["model_version"],
        feature_columns=meta["feature_columns"],
        training_rows=meta["training_rows"],
        validation_rows=meta["validation_rows"],
        model_size_kb=meta["model_size_kb"],
        benchmark_results=meta["benchmark_results"],
    )


@app.post("/predict", response_model=PredictionResponse)
def predict(request: PredictionRequest):
    if not model_service.is_loaded:
        raise HTTPException(status_code=503, detail="Model not loaded")
    try:
        return model_service.predict(request)
    except Exception as exc:  # noqa: BLE001 — turn any inference failure into a clean 500
        logger.exception("Prediction failed")
        raise HTTPException(status_code=500, detail=f"Prediction failed: {exc}") from exc