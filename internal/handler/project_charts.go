package handler

import (
	"sort"
	"time"

	"github.com/saschadaemgen/GoLab/internal/model"
)

// Sprint 16b visual polish: server-side builders that turn raw store
// aggregates into Chart.js-ready datasets. Templates emit the result
// as JSON via the {{ jsonData ... }} helper; the matching Alpine
// component on the page reads it and instantiates Chart.js.
//
// Why server-side: Chart.js takes labels + datasets in a specific
// shape. Doing the pivot in the template would require non-trivial
// loops over indexed data; doing it in the Alpine component would
// duplicate the data-massage logic per page. One pure Go function
// per chart keeps the responsibility in one place.

// ============================================================
// Space activity (Phase 2b)
// ============================================================

// spaceActivityChart is the Chart.js dataset for the stacked-bar
// "posts per week, by project" chart on the Space landing page.
type spaceActivityChart struct {
	Labels   []string               `json:"labels"`
	Datasets []spaceActivityDataset `json:"datasets"`
	HasData  bool                   `json:"hasData"`
}

type spaceActivityDataset struct {
	Label           string `json:"label"`
	BackgroundColor string `json:"backgroundColor"`
	Data            []int  `json:"data"`
}

// buildSpaceActivityChart pivots WeeklyProjectCount rows into a
// stacked-bar dataset. Empty when there is nothing to show; the
// template skips rendering the canvas in that case.
func buildSpaceActivityChart(weekly []model.WeeklyProjectCount, projects []model.ProjectWithStats) spaceActivityChart {
	if len(weekly) == 0 || len(projects) == 0 {
		return spaceActivityChart{HasData: false}
	}

	// Collect distinct weeks. Postgres date_trunc returns midnight UTC
	// for week boundaries (Monday-anchored).
	seen := map[time.Time]struct{}{}
	for _, w := range weekly {
		seen[w.WeekStart] = struct{}{}
	}
	weeks := make([]time.Time, 0, len(seen))
	for w := range seen {
		weeks = append(weeks, w)
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Before(weeks[j]) })

	labels := make([]string, len(weeks))
	weekIndex := make(map[time.Time]int, len(weeks))
	for i, w := range weeks {
		labels[i] = w.Format("Jan 2")
		weekIndex[w] = i
	}

	// One dataset per project (skip projects with no posts this window).
	datasets := make([]spaceActivityDataset, 0, len(projects))
	for _, p := range projects {
		ds := spaceActivityDataset{
			Label:           p.Name,
			BackgroundColor: chartProjectColor(p.Color),
			Data:            make([]int, len(weeks)),
		}
		any := false
		for _, w := range weekly {
			if w.ProjectID != p.ID {
				continue
			}
			if i, ok := weekIndex[w.WeekStart]; ok {
				ds.Data[i] = w.Count
				if w.Count > 0 {
					any = true
				}
			}
		}
		if any {
			datasets = append(datasets, ds)
		}
	}

	return spaceActivityChart{
		Labels:   labels,
		Datasets: datasets,
		HasData:  len(datasets) > 0,
	}
}

// chartProjectColor falls back to the SimpleGo accent when a project
// hasn't been given an explicit color. Centralises the default so
// every chart shows the same shade for unconfigured projects.
func chartProjectColor(c string) string {
	if c == "" {
		return "#45BDD1"
	}
	return c
}

// ============================================================
// Project dashboard (Phase 2c)
// ============================================================

// projectSeasonsChart is the bar chart "posts per season" on the
// project landing page. Single dataset; the active season's bar gets
// the accent color, others get a muted variant.
type projectSeasonsChart struct {
	Labels          []string `json:"labels"`
	Data            []int    `json:"data"`
	BackgroundColor []string `json:"backgroundColor"`
	HasData         bool     `json:"hasData"`
}

func buildProjectSeasonsChart(seasons []model.SeasonPostCount, accent string) projectSeasonsChart {
	out := projectSeasonsChart{HasData: len(seasons) > 0}
	if !out.HasData {
		return out
	}
	out.Labels = make([]string, len(seasons))
	out.Data = make([]int, len(seasons))
	out.BackgroundColor = make([]string, len(seasons))
	for i, s := range seasons {
		out.Labels[i] = "S" + itoa(s.SeasonNumber)
		out.Data[i] = s.PostCount
		switch s.Status {
		case model.SeasonStatusActive:
			out.BackgroundColor[i] = chartProjectColor(accent)
		case model.SeasonStatusClosed:
			out.BackgroundColor[i] = "rgba(155, 89, 182, 0.5)"
		default:
			out.BackgroundColor[i] = "rgba(149, 165, 166, 0.4)"
		}
	}
	return out
}

// projectHeatmap is the GitHub-style 12 × 7 grid of post counts per
// day for the last 84 days. Cells are buckets [date, count, level]
// where level is 0-4 for the five shade ranges in the briefing.
type projectHeatmap struct {
	Cells   []heatmapCell `json:"cells"`
	WeekTop time.Time     `json:"weekTop"`
	HasData bool          `json:"hasData"`
}

type heatmapCell struct {
	Date  string `json:"date"`  // YYYY-MM-DD
	Count int    `json:"count"`
	Level int    `json:"level"` // 0-4
	Col   int    `json:"col"`   // 0-11 (week column)
	Row   int    `json:"row"`   // 0-6 (day-of-week, Mon=0)
	X     int    `json:"x"`     // Col * cell stride, pre-baked for the SVG <rect>
	Y     int    `json:"y"`     // Row * cell stride
}

// buildProjectHeatmap fills an 84-cell grid (12 weeks × 7 days)
// keyed by date. The grid is anchored on today and walks backwards;
// missing days from the daily query default to count 0.
func buildProjectHeatmap(daily []model.DailyCount) projectHeatmap {
	byDay := make(map[string]int, len(daily))
	for _, d := range daily {
		byDay[d.Day.Format("2006-01-02")] = d.Count
	}

	// Anchor: today, normalised to UTC midnight to match the SQL
	// date_trunc('day') buckets.
	today := time.Now().UTC().Truncate(24 * time.Hour)

	// Walk back 83 days (84 total including today). Layout: oldest
	// week on the left (col 0), today on the right.
	cells := make([]heatmapCell, 0, 84)
	hasData := false
	for i := 83; i >= 0; i-- {
		d := today.AddDate(0, 0, -i)
		key := d.Format("2006-01-02")
		count := byDay[key]
		if count > 0 {
			hasData = true
		}
		col := 11 - i/7  // 0..11
		row := int(d.Weekday())
		// Re-anchor to Monday=0 (Sunday is 0 in time.Weekday).
		row = (row + 6) % 7
		cells = append(cells, heatmapCell{
			Date:  key,
			Count: count,
			Level: heatmapLevel(count),
			Col:   col,
			Row:   row,
			X:     col * 14, // 12px cell + 2px gap = 14px stride
			Y:     row * 14,
		})
	}

	return projectHeatmap{
		Cells:   cells,
		WeekTop: today.AddDate(0, 0, -83),
		HasData: hasData,
	}
}

// heatmapLevel maps a post count to a shade bucket per the
// addendum's color levels:
//
//	0 posts   -> level 0
//	1-2 posts -> level 1
//	3-5 posts -> level 2
//	6-9 posts -> level 3
//	10+ posts -> level 4
func heatmapLevel(n int) int {
	switch {
	case n == 0:
		return 0
	case n <= 2:
		return 1
	case n <= 5:
		return 2
	case n <= 9:
		return 3
	default:
		return 4
	}
}

// ============================================================
// Season dashboard (Phase 2d)
// ============================================================

// seasonDailyChart is the line chart "posts over time, last 30 days"
// on the season detail page. Days with zero posts still get rendered
// so the line spans the full window.
type seasonDailyChart struct {
	Labels  []string `json:"labels"`
	Data    []int    `json:"data"`
	HasData bool     `json:"hasData"`
}

func buildSeasonDailyChart(daily []model.DailyCount) seasonDailyChart {
	byDay := make(map[string]int, len(daily))
	for _, d := range daily {
		byDay[d.Day.Format("2006-01-02")] = d.Count
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	labels := make([]string, 30)
	data := make([]int, 30)
	hasData := false
	for i := 0; i < 30; i++ {
		d := today.AddDate(0, 0, i-29) // i=0 -> 29 days ago, i=29 -> today
		labels[i] = d.Format("Jan 2")
		key := d.Format("2006-01-02")
		data[i] = byDay[key]
		if data[i] > 0 {
			hasData = true
		}
	}
	return seasonDailyChart{
		Labels:  labels,
		Data:    data,
		HasData: hasData,
	}
}

// seasonTypeChart is the donut breakdown by post type. Order of
// slices is fixed (the same order the compose box uses) so the
// legend stays stable across views.
type seasonTypeChart struct {
	Labels          []string `json:"labels"`
	Data            []int    `json:"data"`
	BackgroundColor []string `json:"backgroundColor"`
	HasData         bool     `json:"hasData"`
}

var seasonTypeOrder = []string{
	"discussion", "question", "tutorial", "code",
	"showcase", "link", "announcement",
}

var seasonTypeColors = map[string]string{
	"discussion":   "#45BDD1",
	"question":     "#9B59B6",
	"tutorial":     "#2ECC71",
	"code":         "#F39C12",
	"showcase":     "#E67E22",
	"link":         "#95A5A6",
	"announcement": "#E74C3C",
}

func buildSeasonTypeChart(byType map[string]int) seasonTypeChart {
	out := seasonTypeChart{}
	for _, t := range seasonTypeOrder {
		n := byType[t]
		if n == 0 {
			continue
		}
		out.Labels = append(out.Labels, t)
		out.Data = append(out.Data, n)
		out.BackgroundColor = append(out.BackgroundColor, seasonTypeColors[t])
	}
	out.HasData = len(out.Data) > 0
	return out
}

// itoa is a tiny strconv-free integer formatter used in chart labels.
// strconv.Itoa would do the same job, but the package is already
// imported by other files; importing it here for one call would
// noise the import block. Plain manual conversion keeps things tidy.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
