package chart

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A query matching hundreds of series must yield a readable chart, not a
// legend covering the whole canvas.
func TestQueryRangeCapsSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[`)
		for i := 0; i < 20; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"metric":{"instance":"node%02d"},"values":[[1720000000,"1"]]}`, i)
		}
		fmt.Fprint(w, `]}}`)
	}))
	defer srv.Close()

	series, err := New(srv.URL).QueryRange(context.Background(), "up", time.Unix(1720000000, 0), time.Unix(1720000100, 0))
	require.NoError(t, err)
	assert.Len(t, series, maxSeries)
	// Deterministic selection: sorted by label, so re-renders of the same
	// alert look the same.
	assert.Less(t, series[0].Label, series[1].Label)
}

func TestQueryRangeEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[]}}`)
	}))
	defer srv.Close()
	// "No data" must be an error (→ text-only notification), not an empty chart.
	_, err := New(srv.URL).QueryRange(context.Background(), "absent_metric", time.Now().Add(-time.Hour), time.Now())
	require.ErrorContains(t, err, "no data")
}

func TestChartForAlertEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The expression from the generatorURL must arrive as the query.
		assert.Equal(t, `up{job="x"}`, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"metric":{"__name__":"up"},"values":[[1720000000,"1"],[1720000060,"0"]]}]}}`)
	}))
	defer srv.Close()

	png, expr, err := New(srv.URL).ChartForAlert(context.Background(),
		"http://prom/graph?g0.expr=up%7Bjob%3D%22x%22%7D&g0.tab=1", time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, `up{job="x"}`, expr)
	assert.True(t, len(png) > 1000 && string(png[1:4]) == "PNG")
}

func TestLabelString(t *testing.T) {
	// Label rendering drives the legend; each shape must stay identifiable.
	assert.Equal(t, `up{instance="a",job="b"}`, labelString(map[string]string{"__name__": "up", "job": "b", "instance": "a"}))
	assert.Equal(t, "up", labelString(map[string]string{"__name__": "up"}))
	assert.Equal(t, `{instance="a"}`, labelString(map[string]string{"instance": "a"}))
	assert.Equal(t, "value", labelString(map[string]string{}))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "short", truncate("short", 10))
	got := truncate("0123456789abcdef", 10)
	assert.LessOrEqual(t, len([]rune(got)), 10)
	assert.Contains(t, got, "…")
}

func TestRenderPadsShorterSeries(t *testing.T) {
	// Misaligned series lengths (scrape gaps) must not panic or skew columns.
	now := time.Now()
	long := Series{Label: "long"}
	for i := 0; i < 30; i++ {
		long.Times = append(long.Times, now.Add(time.Duration(i)*time.Minute))
		long.Values = append(long.Values, float64(i))
	}
	short := Series{Label: "short", Times: long.Times[:5], Values: long.Values[:5]}
	png, err := Render("test", []Series{long, short})
	require.NoError(t, err)
	assert.True(t, len(png) > 1000)
}
