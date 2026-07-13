// Package chart renders Prometheus range queries as PNG charts for
// alertmanager notifications.
package chart

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	gocharts "github.com/vicanso/go-charts/v2"
)

const (
	// window is how much history the chart shows before now.
	window = 30 * time.Minute
	// maxSeries keeps charts readable when a query matches many series.
	maxSeries = 8
	steps     = 200
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ExprFromGeneratorURL extracts the PromQL expression from an alert's
// generatorURL (".../graph?g0.expr=<query>&g0.tab=1").
func ExprFromGeneratorURL(generatorURL string) (string, error) {
	u, err := url.Parse(generatorURL)
	if err != nil {
		return "", fmt.Errorf("parsing generatorURL: %w", err)
	}
	expr := u.Query().Get("g0.expr")
	if expr == "" {
		return "", fmt.Errorf("no g0.expr parameter in generatorURL %q", generatorURL)
	}
	return expr, nil
}

// ChartForAlert queries the alert's expression over the recent window and
// renders it. startsAt widens the window so the alert's onset is visible.
func (c *Client) ChartForAlert(ctx context.Context, generatorURL string, startsAt time.Time) ([]byte, string, error) {
	expr, err := ExprFromGeneratorURL(generatorURL)
	if err != nil {
		return nil, "", err
	}
	end := time.Now()
	start := end.Add(-window)
	if !startsAt.IsZero() && startsAt.Before(start) {
		start = startsAt.Add(-5 * time.Minute)
	}
	series, err := c.QueryRange(ctx, expr, start, end)
	if err != nil {
		return nil, expr, err
	}
	png, err := Render(expr, series)
	return png, expr, err
}

// Series is one time series returned by a range query.
type Series struct {
	Label  string
	Times  []time.Time
	Values []float64
}

// QueryRange runs a Prometheus range query.
func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time) ([]Series, error) {
	step := end.Sub(start) / steps
	if step < time.Second {
		step = time.Second
	}
	q := url.Values{
		"query": {expr},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatInt(int64(step.Seconds()), 10)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying prometheus: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding prometheus response: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s", body.Error)
	}

	var series []Series
	for _, res := range body.Data.Result {
		s := Series{Label: labelString(res.Metric)}
		for _, v := range res.Values {
			ts, ok := v[0].(float64)
			if !ok {
				continue
			}
			str, ok := v[1].(string)
			if !ok {
				continue
			}
			val, err := strconv.ParseFloat(str, 64)
			if err != nil {
				continue
			}
			s.Times = append(s.Times, time.Unix(int64(ts), 0))
			s.Values = append(s.Values, val)
		}
		if len(s.Times) > 0 {
			series = append(series, s)
		}
	}
	if len(series) == 0 {
		return nil, fmt.Errorf("query returned no data")
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Label < series[j].Label })
	if len(series) > maxSeries {
		series = series[:maxSeries]
	}
	return series, nil
}

// Render draws the series as a PNG time-series chart (Grafana-style theme).
func Render(title string, series []Series) ([]byte, error) {
	// go-charts wants aligned columns: use the longest series for the X
	// axis and pad the others with nulls.
	longest := 0
	for i := range series {
		if len(series[i].Times) > len(series[longest].Times) {
			longest = i
		}
	}
	xLabels := make([]string, len(series[longest].Times))
	for i, t := range series[longest].Times {
		xLabels[i] = t.Format("15:04")
	}

	const (
		width       = 1100
		height      = 440
		legendWidth = width / 5 // legend column on the right: 20% of the canvas
	)

	values := make([][]float64, len(series))
	for i, s := range series {
		row := make([]float64, len(xLabels))
		for j := range row {
			if j < len(s.Values) {
				row[j] = s.Values[j]
			} else {
				row[j] = gocharts.GetNullValue()
			}
		}
		values[i] = row
	}

	// go-charts' built-in vertical legend cannot be pinned to the side (its
	// wrap logic bounces overflowing items back to x=0), so the layout is
	// composed manually: the chart renders into the left 80% of the canvas
	// with its legend hidden, and the legend column is drawn directly.
	theme := gocharts.NewTheme(gocharts.ThemeGrafana)
	p, err := gocharts.NewPainter(gocharts.PainterOptions{
		Type:   gocharts.ChartOutputPNG,
		Width:  width,
		Height: height,
	})
	if err != nil {
		return nil, fmt.Errorf("creating painter: %w", err)
	}
	p.SetBackground(width, height, theme.GetBackgroundColor())

	// Legend column: one line-dot + label per series, top-aligned under the
	// title line. ~8px per character at this font size keeps labels inside
	// the column.
	maxChars := (legendWidth - 52) / 8
	legendX := width - legendWidth + 4
	p.SetTextStyle(gocharts.Style{FontColor: theme.GetTextColor(), FontSize: 11})
	y := 40
	for i, s := range series {
		color := theme.GetSeriesColor(i)
		p.SetDrawingStyle(gocharts.Style{FillColor: color, StrokeColor: color})
		p.Rect(gocharts.Box{Top: y + 4, Left: legendX, Right: legendX + 18, Bottom: y + 14})
		p.Text(truncate(s.Label, maxChars), legendX+24, y+15)
		y += 22
	}

	seriesList := gocharts.NewSeriesListDataFromValues(values, gocharts.ChartTypeLine)
	for i := range seriesList {
		seriesList[i].Style.StrokeWidth = 1.6
	}
	_, err = gocharts.Render(gocharts.ChartOption{
		Parent:     p,
		Box:        gocharts.Box{Right: width - legendWidth, Bottom: height},
		Theme:      gocharts.ThemeGrafana,
		Type:       gocharts.ChartOutputPNG,
		Width:      width - legendWidth,
		Height:     height,
		Padding:    gocharts.Box{Top: 10, Left: 10, Right: 10, Bottom: 10},
		Title:      gocharts.TitleOption{Text: truncate(title, 110)},
		XAxis:      gocharts.XAxisOption{Data: xLabels},
		SeriesList: seriesList,
		Legend:     gocharts.LegendOption{Show: gocharts.FalseFlag()},
		ValueFormatter: func(f float64) string {
			return strconv.FormatFloat(f, 'g', 4, 64)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("rendering chart: %w", err)
	}

	png, err := p.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encoding chart: %w", err)
	}
	return png, nil
}

func labelString(metric map[string]string) string {
	name := metric["__name__"]
	keys := make([]string, 0, len(metric))
	for k := range metric {
		if k != "__name__" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, metric[k]))
	}
	if len(pairs) == 0 {
		if name == "" {
			return "value"
		}
		return name
	}
	return fmt.Sprintf("%s{%s}", name, strings.Join(pairs, ","))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
