# FindDoctors Aggregator Server (Golang) 🏥

A high-performance, type-safe backend aggregator for the Hellenic Health Appointment System (`finddoctors.gov.gr`). 

## Overview
This server acts as a specialized proxy that fixes the primary limitation of the official health portal: the inability to search across different entities (Public Hospitals vs. Primary Care Centers) and regions simultaneously.

### Key Features
- **Parallel Cross-Entity Search**: Queries both Public Hospitals (`foreas: 1`) and Primary Health Centers (`foreas: 18`) concurrently using Goroutines, merging results into a unified view.
- **Emergency Triage Finder**: Geolocation-aware search that finds the closest available appointment for any specialty, sorted by distance and date.
- **Fast Scanner Engine**: Uses a concurrent worker pool (semaphore pattern) to probe multiple health units for their `firstavailableslot` without loading heavy availability grids.
- **Metadata Proxy & Caching**: Fast, cached access to medical specialties and prefectures.
- **Zero-Maintenance Architecture**: Uses dynamic ID discovery and polymorphic JSON handling (via `json.RawMessage`) to survive inconsistent upstream API changes.
- **TDD-Backed**: 100% test coverage for core logic and API bindings using `httptest` mocks.

## 🛠️ Tech Stack
- **Language**: Go 1.22+
- **Concurrency**: Goroutines, Channels, Sync Mutexes/WaitGroups.
- **Testing**: Native `go test` with `-race` detection.

## 🚀 API Scenarios

### 1. Metadata Proxy
Fetch all available specialties with zero lag.
`GET /api/specialties`

**Response:** 
```json
[{"id": 6, "name": "ΔΕΡΜΑΤΟΛΟΓΟΣ-ΑΦΡΟΔΙΣΙΟΛΟΓΟΣ"}, ...]
```

### 2. Emergency Finder
Find the closest Neurologist near Athens:
`GET /api/emergency?specialtyId=10&lat=37.9838&lon=23.7275`

**Response:** 
```json
[{"hunit": 70600, "name": "Γ.Ν.Α 'Κ.Α.Τ.'", "firstDate": "2026-03-24", "latitude": 38.0673, "longitude": 23.8041}]
```

### 3. Nationwide specialist Hunt
Find the next available Urologist anywhere in Greece:
`GET /api/nationwide?specialtyId=12`

**Response:**
```json
{"count": 15, "results": [{"hunit": 12345, "name": "K.Y. ΑΛΕΞΑΝΔΡΑΣ", "firstDate": "2026-04-01"}]}
```

### 4. Hospital Capacity Heatmap
Find the weekly appointment fill-rate across all medical specialties for a specific health unit.
`GET /api/hospitals/70600/capacity?prefectureId=11`

**Response:**
```json
{"hunitId": 70600, "scanned": 46, "specialties": [{"specialityId": 22, "name": "ΑΓΓΕΙΟΧΕΙΡΟΥΡΓΟΣ", "fillRate": 85.5}, {"specialityId": 14, "name": "ΠΑΘΟΛΟΓΟΣ", "fillRate": 100}]}
```

## 🏗️ Development

### Prerequisites
- Go 1.22 or higher

### Installation
```bash
git clone https://github.com/angelospk/find_doctors_server.git
cd find_doctors_server
go mod tidy
```

### Running Tests (TDD)
```bash
go test -v -race ./...
```

### Starting the Server
```bash
go run ./cmd/server/main.go
```
The server will be available at `http://localhost:8080`.

## 🔒 Security & Policy
This project is an aggregator for **publicly available** discovery data. It does **not** handle PII (Personally Identifiable Information) or perform actual bookings. Booking remains a client-side responsibility via TaxisNet sessions on the official portal.
