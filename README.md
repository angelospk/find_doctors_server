# FindDoctors Aggregator Server (Golang) 🏥

A high-performance, type-safe backend aggregator for the Hellenic Health Appointment System (`finddoctors.gov.gr`). 

## Overview
This server acts as a specialized proxy that fixes the primary limitation of the official health portal: the inability to search across different entities (Public Hospitals vs. Primary Care Centers) and regions simultaneously.

### Key Features
- **Smart Search (Unified)**: Consolidates nationwide and proximity searches into a single `/api/search` endpoint with "Distance-Filtered Soonest" logic.
- **Parallel Cross-Entity Search**: Queries both Public Hospitals (`foreas: 1`) and Primary Health Centers (`foreas: 18`) concurrently, merging results into a unified view.
- **Actionable Capacity Reports**: Enhanced hospital load reports that include the earliest available appointment (`firstDate`) for every specialty.
- **Fast Scanner Engine**: Uses a concurrent worker pool (semaphore pattern) to probe multiple health units for their `firstavailableslot` with sub-second latency.
- **Type Safety**: End-to-end type safety from Go to TypeScript via `tygo`.
- **TDD-Backed**: 100% test coverage for core logic and aggregator sorting algorithms.

## 🚀 API Endpoints

### 1. Smart Search (Unified)
Find the best appointments based on date and proximity. It prioritizes the **soonest** available slot within a specific **distance**.
`GET /api/search?specialtyId=6&lat=37.9&lon=23.7&maxDistanceInKm=100`

**Parameters:**
- `specialtyId` (Required): The ID of the medical specialty.
- `lat`, `lon` (Optional): User coordinates for distance calculation and proximity sorting.
- `maxDistanceInKm` (Optional): Radius filter. If set, units further than this will be excluded.
- `prefectureId` (Optional): Filter results to a specific region.

**Response:** 
```json
[
  {
    "hunitId": "70600",
    "name": "Γ.Ν.Α 'Κ.Α.Τ.'",
    "firstDate": "2026-03-24",
    "latitude": 38.0673,
    "longitude": 23.8041,
    "foreasId": 1,
    "city": "ΚΗΦΙΣΙΑ"
  }
]
```

### 2. Hospital Capacity & Analytics
Get a detailed fill-rate report for a hospital, including actionable "Next Available" dates.
`GET /api/hospitals/{hunitId}/capacity`

**Optional Filter:** `?specialtyId=X` to get data only for one department.

**Response:**
```json
{
  "hunitId": 70600,
  "scanned": 46,
  "specialties": [
    {
      "specialtyId": 22,
      "name": "ΑΓΓΕΙΟΧΕΙΡΟΥΡΓΟΣ",
      "fillRate": 85.5,
      "firstDate": "2026-04-12"
    },
    {
      "specialtyId": 14,
      "name": "ΠΑΘΟΛΟΓΟΣ",
      "fillRate": 100,
      "firstDate": null
    }
  ]
}
```

### 3. Granular Appointment Slots
Fetch detailed available times, doctor names, and clinic metadata for a specific unit and date.
`GET /api/hospitals/{hunitId}/slots?specialtyId=X&date=YYYY-MM-DD&prefectureId=Y&foreasId=Z`

**Parameters:**
- `specialtyId` (Required): The ID of the medical specialty.
- `date` (Required): Targeted date for appointments (YYYY-MM-DD).
- `prefectureId` (Required): The regional code (necessary for granular lookups).
- `foreasId` (Required): Unit type (1 for Hospitals, 18 for Health Centers).

**Response:**
```json
[
  {
    "hunitId": 718,
    "time": "10:45",
    "date": "2026-03-26T10:45:00.000+0200",
    "dayOfWeek": 4,
    "docName": "ΚΩΝΣΤΑΝΤΙΝΙΔΗΣ ΑΡΙΣΤΕΙΔΗΣ",
    "address": "6ο χλμ Εθνικής Οδού Αλεξανδρούπολης",
    "city": "ΑΛΕΞΑΝΔΡΟΥΠΟΛΗ",
    "comments": "ΠΡΟΣΟΧΗ ΤΟ ΙΑΤΡΕΙΟ ΕΞΕΤΑΖΕΙ ΠΑΙΔΙΑ...",
    "rvtName": "Τακτικό"
  }
]
```

### 4. Metadata Discovery
`GET /api/specialties` - Returns all medical specialties.

## 🏗️ Development

### Running Tests (TDD)
We follow a strict TDD workflow. Run tests to verify search and sorting logic:
```bash
go test -v -race ./...
```

### Type Generation
To sync types with the frontend:
```bash
tygo generate
```

### Starting the Server
```bash
go run ./cmd/server/main.go
```
The server will be available at `http://localhost:8080`.

## 🔒 Security

- **Public Access**: Authentication is **not required** for read-only discovery endpoints:
    - `/api/search`
    - `/api/specialties`
    - `/api/hospitals/{hunitId}/capacity`
- **Ministry API Integration**: This aggregator interacts with the official portal using their public `Authorization: no-auth` protocol.
- **Privacy Model**: No user cookies or session data are stored. The system is designed for stateless discovery only.
