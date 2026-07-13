package chart

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRenderLayoutSample writes a sample chart PNG for eyeballing layout
// changes without firing real alerts. Skipped unless CHART_SAMPLE_OUT is
// set:
//
//	CHART_SAMPLE_OUT=/tmp/sample.png go test -run TestRenderLayoutSample ./internal/chart/
func TestRenderLayoutSample(t *testing.T) {
	out := os.Getenv("CHART_SAMPLE_OUT")
	if out == "" {
		t.Skip("set CHART_SAMPLE_OUT to render a layout sample")
	}
	now := time.Now()
	mk := func(base, wobble float64) Series {
		s := Series{}
		for i := 0; i < 60; i++ {
			s.Times = append(s.Times, now.Add(time.Duration(i-60)*time.Minute))
			s.Values = append(s.Values, base+wobble*float64(i%9))
		}
		return s
	}
	s1 := mk(10, 1.5)
	s1.Label = `rate(node_cpu_seconds_total{instance="node1.example.org:9100",mode="user"}[1m])`
	s2 := mk(20, 0.8)
	s2.Label = `rate(node_cpu_seconds_total{instance="node2.example.org:9100",mode="user"}[1m])`
	s3 := mk(5, 2.2)
	s3.Label = `rate(node_cpu_seconds_total{instance="node3.example.org:9100",mode="user"}[1m])`

	s4 := mk(30, 1.1)
	s4.Label = `up{instance="node4.example.org:9100",job="node"}`
	png, err := Render(`rate(node_cpu_seconds_total{mode="user"}[1m]) — long title`, []Series{s4, s1, s2, s3})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(out, png, 0o644))
	t.Logf("sample written to %s", out)
}
