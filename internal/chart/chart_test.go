package chart

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExprFromGeneratorURL(t *testing.T) {
	// Alertmanager's generatorURL carries the firing expression; that's the
	// only thing linking an alert back to queryable data.
	expr, err := ExprFromGeneratorURL("http://prometheus:9090/graph?g0.expr=up%7Bjob%3D%22node%22%7D&g0.tab=1")
	require.NoError(t, err)
	assert.Equal(t, `up{job="node"}`, expr)

	_, err = ExprFromGeneratorURL("http://prometheus:9090/graph")
	assert.Error(t, err)
}

func TestQueryRangeAndRender(t *testing.T) {
	// Fake Prometheus returning two series.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query_range", r.URL.Path)
		assert.Equal(t, "up", r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"up","instance":"a:9100"},"values":[[1720000000,"1"],[1720000060,"1"]]},
			{"metric":{"__name__":"up","instance":"b:9100"},"values":[[1720000000,"1"],[1720000060,"0"]]}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	series, err := c.QueryRange(context.Background(), "up", time.Unix(1720000000, 0), time.Unix(1720000120, 0))
	require.NoError(t, err)
	require.Len(t, series, 2)
	// Labels must be distinguishable or a multi-instance chart is useless.
	assert.NotEqual(t, series[0].Label, series[1].Label)

	png, err := Render("up", series)
	require.NoError(t, err)
	// A real PNG comes back, not an empty buffer or an error page.
	assert.True(t, bytes.HasPrefix(png, []byte("\x89PNG")), "output must be a PNG")
	assert.Greater(t, len(png), 1000)
}

func TestQueryRangeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL).QueryRange(context.Background(), "up", time.Now().Add(-time.Hour), time.Now())
	require.ErrorContains(t, err, "bad query")
}
