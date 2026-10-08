// cmd/simulator/main.go — Trip Simulator (spec §18).
//
// Reads historical trip rows from the Kaggle CSV and replays them as
// "live" events against the Go backend's POST /api/v1/trips, at a
// configurable speed multiplier (1x = real historical pace, 10x/100x =
// compressed). This is what turns a static, already-happened dataset
// into something that exercises the concurrency/worker-pool/dispatch
// machinery the way real live traffic would.
//
// Design: a single goroutine paces itself against each row's
// pickup_datetime (scaled by 1/speed) and hands the row off to a bounded
// worker pool of sender goroutines — same "don't spawn one goroutine per
// unit of work" discipline as the main server's prediction worker pool.
//
// Run:
//
//	cd backend
//	go run ./simulator/cmd/simulator --file ../ml-service/data/train.csv --speed 100 --limit 500 --target http://localhost:8080
package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type tripRow struct {
	ID             string
	PickupDatetime time.Time
	PassengerCount int
	PickupLat      float64
	PickupLng      float64
	DropoffLat     float64
	DropoffLng     float64
}

func main() {
	filePath := flag.String("file", "", "path to the Kaggle train.csv (required)")
	speed := flag.Float64("speed", 10, "replay speed multiplier (1 = real historical pace, 100 = 100x faster)")
	limit := flag.Int("limit", 500, "max number of trips to replay")
	target := flag.String("target", "http://localhost:8080", "base URL of the Go backend")
	workers := flag.Int("workers", 20, "number of concurrent sender goroutines")
	flag.Parse()

	if *filePath == "" {
		log.Fatal("--file is required (path to train.csv)")
	}

	rows, err := loadRows(*filePath, *limit)
	if err != nil {
		log.Fatalf("failed to load rows: %v", err)
	}
	if len(rows) == 0 {
		log.Fatal("no rows loaded — check --file and --limit")
	}
	log.Printf("loaded %d trips from %s, replaying at %.0fx speed against %s with %d workers",
		len(rows), *filePath, *speed, *target, *workers)

	jobs := make(chan tripRow, *workers*2)
	var wg sync.WaitGroup
	var sent, failed int64

	httpClient := &http.Client{Timeout: 5 * time.Second}

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for row := range jobs {
				if err := sendTrip(httpClient, *target, row); err != nil {
					atomic.AddInt64(&failed, 1)
				} else {
					atomic.AddInt64(&sent, 1)
				}
			}
		}(i)
	}

	// pacing goroutine: sleeps between rows according to their real
	// historical gaps, scaled down by the speed multiplier, then hands
	// the row to the worker pool via the jobs channel.
	baseTime := rows[0].PickupDatetime
	replayStart := time.Now()
	for _, row := range rows {
		elapsed := row.PickupDatetime.Sub(baseTime)
		targetOffset := time.Duration(float64(elapsed) / *speed)
		sleepFor := targetOffset - time.Since(replayStart)
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}
		jobs <- row
	}
	close(jobs)
	wg.Wait()

	log.Printf("done: %d sent, %d failed, total wall time %s", sent, failed, time.Since(replayStart).Round(time.Millisecond))
}

func loadRows(path string, limit int) ([]tripRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(bufio.NewReader(f))
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := columnIndex(header)

	// Read every row first (not just up to `limit`): the Kaggle CSV is NOT
	// sorted by pickup_datetime — rows span Jan-Jun 2016 in arbitrary
	// order even within the first few hundred lines. Taking the file's
	// first N rows and THEN sorting would give a random 6-month-wide
	// sample, making any speed multiplier meaningless (the gaps between
	// consecutive replayed trips would themselves be random). Instead we
	// sort the full dataset by time first, then take `limit` CONSECUTIVE
	// rows from it — a real, tightly-clustered slice of history, exactly
	// what the speed multiplier is meant to compress.
	var rows []tripRow
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}
		row, err := parseRow(record, col)
		if err != nil {
			continue // skip malformed rows rather than aborting the whole replay
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].PickupDatetime.Before(rows[j].PickupDatetime)
	})

	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	return rows, nil
}

func columnIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[name] = i
	}
	return idx
}

func parseRow(record []string, col map[string]int) (tripRow, error) {
	get := func(name string) string { return record[col[name]] }

	pickupTime, err := time.Parse("2006-01-02 15:04:05", get("pickup_datetime"))
	if err != nil {
		return tripRow{}, err
	}
	passengerCount, err := strconv.Atoi(get("passenger_count"))
	if err != nil {
		return tripRow{}, err
	}
	pickupLng, err := strconv.ParseFloat(get("pickup_longitude"), 64)
	if err != nil {
		return tripRow{}, err
	}
	pickupLat, err := strconv.ParseFloat(get("pickup_latitude"), 64)
	if err != nil {
		return tripRow{}, err
	}
	dropoffLng, err := strconv.ParseFloat(get("dropoff_longitude"), 64)
	if err != nil {
		return tripRow{}, err
	}
	dropoffLat, err := strconv.ParseFloat(get("dropoff_latitude"), 64)
	if err != nil {
		return tripRow{}, err
	}

	return tripRow{
		ID:             get("id"),
		PickupDatetime: pickupTime,
		PassengerCount: passengerCount,
		PickupLat:      pickupLat,
		PickupLng:      pickupLng,
		DropoffLat:     dropoffLat,
		DropoffLng:     dropoffLng,
	}, nil
}

func sendTrip(client *http.Client, target string, row tripRow) error {
	body, err := json.Marshal(map[string]any{
		"pickup_lat":      row.PickupLat,
		"pickup_lng":      row.PickupLng,
		"dropoff_lat":     row.DropoffLat,
		"dropoff_lng":     row.DropoffLng,
		"passenger_count": row.PassengerCount,
	})
	if err != nil {
		return err
	}

	resp, err := client.Post(target+"/api/v1/trips", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("backend returned status %d for trip %s", resp.StatusCode, row.ID)
	}
	return nil
}
