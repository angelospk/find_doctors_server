package aggregator

import (
	"context"
	"sort"
	"time"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// PrefectureStats holds aggregated fill-rate data for one prefecture.
// tygo:generate
type PrefectureStats struct {
	PrefectureID int     `json:"prefectureId"`
	UnitCount    int     `json:"unitCount"`
	AvgFillRate  float64 `json:"avgFillRate"`
	FirstDate    *string `json:"firstDate"`
}

// HeatmapReport is the top-level response for the nationwide heatmap endpoint.
// tygo:generate
type HeatmapReport struct {
	SpecialtyID int               `json:"specialtyId"`
	Prefectures []PrefectureStats `json:"prefectures"`
}

// NationwideHeatmap aggregates hospital fill-rates at the prefecture level for a given specialty.
// Fill-rate per prefecture = (units with no available slot / total units) * 100.
// Prefectures are sorted by AvgFillRate ascending (most open first).
func (a *Aggregator) NationwideHeatmap(ctx context.Context, specialtyID int) (HeatmapReport, error) {
	now := time.Now().UTC()
	startStr := now.Format("2006-01-02T15:04:05.000Z")
	endStr := now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z")

	payload := ministry.SearchPayload{
		StartDate:    startStr,
		EndDate:      endStr,
		SpecialityID: specialtyID,
	}

	units, err := a.SearchUnified(ctx, payload)
	if err != nil {
		return HeatmapReport{}, err
	}

	scanned := a.FastScanner(ctx, units, payload)

	// Group scanned units by prefecture ID (nil Prefecture maps to 0)
	type groupAccum struct {
		count     int
		nilDates  int
		firstDate *string
	}
	groups := make(map[int]*groupAccum)

	for _, u := range scanned {
		prefID := 0
		if u.Prefecture != nil {
			prefID = *u.Prefecture
		}

		g, ok := groups[prefID]
		if !ok {
			g = &groupAccum{}
			groups[prefID] = g
		}

		g.count++
		if u.FirstDate == nil {
			g.nilDates++
		} else {
			if g.firstDate == nil || *u.FirstDate < *g.firstDate {
				g.firstDate = u.FirstDate
			}
		}
	}

	prefectures := make([]PrefectureStats, 0, len(groups))
	for prefID, g := range groups {
		fillRate := 0.0
		if g.count > 0 {
			fillRate = (float64(g.nilDates) / float64(g.count)) * 100.0
		}
		prefectures = append(prefectures, PrefectureStats{
			PrefectureID: prefID,
			UnitCount:    g.count,
			AvgFillRate:  fillRate,
			FirstDate:    g.firstDate,
		})
	}

	sort.Slice(prefectures, func(i, j int) bool {
		if prefectures[i].AvgFillRate != prefectures[j].AvgFillRate {
			return prefectures[i].AvgFillRate < prefectures[j].AvgFillRate
		}
		return prefectures[i].PrefectureID < prefectures[j].PrefectureID
	})

	return HeatmapReport{
		SpecialtyID: specialtyID,
		Prefectures: prefectures,
	}, nil
}
