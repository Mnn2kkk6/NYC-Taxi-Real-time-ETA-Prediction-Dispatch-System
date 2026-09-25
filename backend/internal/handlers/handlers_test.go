package handlers

import (
	"testing"

	"nyctaxi/backend/internal/models"
)

func validRequest() models.PredictionRequest {
	return models.PredictionRequest{
		PickupLatitude:   40.761,
		PickupLongitude:  -73.982,
		DropoffLatitude:  40.730,
		DropoffLongitude: -73.995,
		PassengerCount:   2,
		PickupDatetime:   "2016-03-15T18:30:00",
	}
}

func TestValidatePredictionRequest_Valid(t *testing.T) {
	if err := validatePredictionRequest(validRequest()); err != nil {
		t.Fatalf("expected valid request to pass, got error: %v", err)
	}
}

func TestValidatePredictionRequest_OutOfNYCBoundingBox(t *testing.T) {
	req := validRequest()
	req.PickupLatitude = 32.18 // the known bad row from the raw dataset
	if err := validatePredictionRequest(req); err == nil {
		t.Fatal("expected error for out-of-NYC pickup latitude, got nil")
	}
}

func TestValidatePredictionRequest_PassengerCountZero(t *testing.T) {
	req := validRequest()
	req.PassengerCount = 0
	if err := validatePredictionRequest(req); err == nil {
		t.Fatal("expected error for passenger_count=0, got nil")
	}
}

func TestValidatePredictionRequest_PassengerCountTooHigh(t *testing.T) {
	req := validRequest()
	req.PassengerCount = 9
	if err := validatePredictionRequest(req); err == nil {
		t.Fatal("expected error for passenger_count=9, got nil")
	}
}

func TestValidatePredictionRequest_MissingDatetime(t *testing.T) {
	req := validRequest()
	req.PickupDatetime = ""
	if err := validatePredictionRequest(req); err == nil {
		t.Fatal("expected error for missing pickup_datetime, got nil")
	}
}

func TestParseIntDefault(t *testing.T) {
	cases := []struct {
		in       string
		fallback int
		want     int
	}{
		{"", 20, 20},
		{"5", 20, 5},
		{"not-a-number", 20, 20},
		{"0", 20, 0},
	}
	for _, c := range cases {
		got := parseIntDefault(c.in, c.fallback)
		if got != c.want {
			t.Errorf("parseIntDefault(%q, %d) = %d, want %d", c.in, c.fallback, got, c.want)
		}
	}
}
