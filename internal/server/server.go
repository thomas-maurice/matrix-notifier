package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/thomas-maurice/tocsin/internal/ingest/alertmanager"
	"github.com/thomas-maurice/tocsin/internal/ingest/gitea"
	"github.com/thomas-maurice/tocsin/internal/ingest/gotify"
	"github.com/thomas-maurice/tocsin/internal/ingest/grafana"
	"github.com/thomas-maurice/tocsin/internal/ingest/slack"
	"github.com/thomas-maurice/tocsin/internal/logging"
	"github.com/thomas-maurice/tocsin/internal/metrics"
	"github.com/thomas-maurice/tocsin/internal/notify"
	"github.com/thomas-maurice/tocsin/internal/store"
)

// Health reports whether the bot can deliver (used by /health).
type Health interface {
	Healthy() (bool, string)
}

// Queue is where accepted notifications go: the durable outbox. The
// dispatcher delivers them asynchronously — a 200 from an ingest endpoint
// means "accepted", not "delivered".
type Queue interface {
	Enqueue(ctx context.Context, e *store.OutboxEntry) error
}

// New builds the HTTP handler exposing the ingest endpoints:
//
//	POST /message       Gotify-compatible (X-Gotify-Key / ?token= / Bearer)
//	POST /alertmanager  Prometheus Alertmanager webhook receiver
//	POST /slack         Slack incoming-webhook compatible (?token= — Slack
//	                    senders can't set headers)
//	GET  /health        liveness check reflecting Matrix sync state
//	GET  /metrics       Prometheus metrics
//	GET  /version       Gotify-compatible version probe
//
// Ingest tokens are resolved through the store; each token routes to its
// channel's room. rl may be nil (rate limiting disabled).
func New(log *slog.Logger, health Health, st *store.Store, q Queue, rl *limiters) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(log))

	r.GET("/health", func(c *gin.Context) {
		if ok, reason := health.Healthy(); ok {
			c.JSON(http.StatusOK, gin.H{"health": "green"})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"health": "red", "reason": reason})
		}
	})
	r.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": "2.6.3", "commit": "tocsin", "buildDate": ""})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/message", handleIngest(st, store.KindGotify, gotify.Parse, q, gotifyResponse, rl))
	r.POST("/alertmanager", handleAlertmanager(st, q, rl))
	giteaHandler := handleIngest(st, store.KindGitea, gitea.Parse, q, nil, rl)
	r.POST("/gitea", giteaHandler)
	r.POST("/forgejo", giteaHandler)
	r.POST("/slack", handleIngest(st, store.KindSlack, slack.Parse, q, slackResponse, rl))
	r.POST("/grafana", handleGrafana(st, q, rl))

	return r
}

// handleAlertmanager queues the formatted notification. When the channel
// has charts enabled AND an alert opted in (annotation `chart: true`), the
// chart target rides along on the entry and the dispatcher renders it at
// send time — best effort, a chart failure degrades to plain text.
func handleAlertmanager(st *store.Store, q Queue, rl *limiters) gin.HandlerFunc {
	const kind = "alertmanager"
	return func(c *gin.Context) {
		token, err := st.ResolveToken(c.Request.Context(), presentedToken(c), store.KindAlertmanager)
		if err != nil {
			metrics.IngestRejected.WithLabelValues(kind, rejectReason(err)).Inc()
			writeTokenError(c, err)
			return
		}
		if !rl.allow(token.Name) {
			metrics.IngestRejected.WithLabelValues(kind, "rate_limit").Inc()
			rateLimited(c, token.Name)
			return
		}
		channel := &token.Channel
		payload, err := alertmanager.ParsePayload(c.Request)
		if err != nil {
			metrics.IngestRejected.WithLabelValues(kind, "parse").Inc()
			c.JSON(http.StatusBadRequest, gin.H{"error": "Bad Request", "errorCode": 400, "errorDescription": err.Error()})
			return
		}
		n := alertmanager.Format(payload)
		applyPrefix(&n, token.Prefix)

		e := queueEntry(channel, kind, n)
		if channel.Chart {
			if target := alertmanager.ChartTarget(payload); target != nil {
				startsAt := target.StartsAt
				e.ChartGeneratorURL = target.GeneratorURL
				e.ChartStartsAt = &startsAt
				e.ChartAlertName = target.Labels["alertname"]
			}
		}
		firing, resolved := alertmanager.Fingerprints(payload)
		correlate(e, firing, resolved)
		if !enqueue(c, q, e) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// handleGrafana queues Grafana unified-alerting webhook notifications,
// carrying alert fingerprints for resolve-by-edit correlation.
func handleGrafana(st *store.Store, q Queue, rl *limiters) gin.HandlerFunc {
	const kind = "grafana"
	return func(c *gin.Context) {
		token, err := st.ResolveToken(c.Request.Context(), presentedToken(c), store.KindGrafana)
		if err != nil {
			metrics.IngestRejected.WithLabelValues(kind, rejectReason(err)).Inc()
			writeTokenError(c, err)
			return
		}
		if !rl.allow(token.Name) {
			metrics.IngestRejected.WithLabelValues(kind, "rate_limit").Inc()
			rateLimited(c, token.Name)
			return
		}
		payload, err := grafana.ParsePayload(c.Request)
		if err != nil {
			metrics.IngestRejected.WithLabelValues(kind, "parse").Inc()
			c.JSON(http.StatusBadRequest, gin.H{"error": "Bad Request", "errorCode": 400, "errorDescription": err.Error()})
			return
		}
		n := grafana.Format(payload)
		applyPrefix(&n, token.Prefix)

		e := queueEntry(&token.Channel, kind, n)
		firing, resolved := grafana.Fingerprints(payload)
		correlate(e, firing, resolved)
		if !enqueue(c, q, e) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// correlate stamps the entry with the payload's fingerprints: firing ones
// get recorded at delivery, resolved ones let the dispatcher update the
// group's existing message in place — including partial resolutions, as
// long as the payload introduces no unannounced firing alert (the
// dispatcher decides; a new alert always posts).
func correlate(e *store.OutboxEntry, firing, resolved []string) {
	if len(firing) > 0 {
		e.Fingerprints = strings.Join(firing, ",")
	}
	if len(resolved) > 0 {
		e.ResolveFingerprints = strings.Join(resolved, ",")
	}
}

func queueEntry(channel *store.Channel, kind string, n notify.Notification) *store.OutboxEntry {
	return &store.OutboxEntry{
		Channel:  channel.Name,
		RoomID:   channel.RoomID,
		Kind:     kind,
		Title:    n.Title,
		Body:     n.Body,
		Priority: n.Priority,
	}
}

// enqueue queues the entry, writing the error response itself when the
// database refuses — the one case where an authenticated ingest request
// still fails.
func enqueue(c *gin.Context, q Queue, e *store.OutboxEntry) bool {
	if err := q.Enqueue(c.Request.Context(), e); err != nil {
		metrics.IngestRejected.WithLabelValues(e.Kind, "enqueue").Inc()
		logging.From(c.Request.Context()).Error("failed to queue notification", "channel", e.Channel, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error", "errorCode": 500, "errorDescription": err.Error()})
		return false
	}
	return true
}

// applyPrefix prepends the token's prefix to the notification title, or to
// the body when there is no title.
func applyPrefix(n *notify.Notification, prefix string) {
	if prefix == "" {
		return
	}
	if n.Title != "" {
		n.Title = prefix + " " + n.Title
	} else {
		n.Body = prefix + " " + n.Body
	}
}

func writeTokenError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized", "errorCode": 401, "errorDescription": "invalid token"})
	} else {
		logging.From(c.Request.Context()).Error("token lookup failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error", "errorCode": 500})
	}
}

func rejectReason(err error) string {
	if errors.Is(err, store.ErrNotFound) {
		return "auth"
	}
	return "error"
}

func rateLimited(c *gin.Context, token string) {
	logging.From(c.Request.Context()).Warn("rate limit exceeded", "token", token)
	c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too Many Requests", "errorCode": 429, "errorDescription": "rate limit exceeded"})
}

type parseFunc func(*http.Request) (notify.Notification, error)
type responseFunc func(*gin.Context, notify.Notification)

func handleIngest(st *store.Store, kind store.TokenKind, parse parseFunc, q Queue, respond responseFunc, rl *limiters) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := st.ResolveToken(c.Request.Context(), presentedToken(c), kind)
		if err != nil {
			metrics.IngestRejected.WithLabelValues(string(kind), rejectReason(err)).Inc()
			writeTokenError(c, err)
			return
		}
		if !rl.allow(token.Name) {
			metrics.IngestRejected.WithLabelValues(string(kind), "rate_limit").Inc()
			rateLimited(c, token.Name)
			return
		}
		n, err := parse(c.Request)
		if err != nil {
			metrics.IngestRejected.WithLabelValues(string(kind), "parse").Inc()
			c.JSON(http.StatusBadRequest, gin.H{"error": "Bad Request", "errorCode": 400, "errorDescription": err.Error()})
			return
		}
		applyPrefix(&n, token.Prefix)
		if !enqueue(c, q, queueEntry(&token.Channel, string(kind), n)) {
			return
		}
		if respond != nil {
			respond(c, n)
		} else {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		}
	}
}

var messageID atomic.Int64

// gotifyResponse mimics the message object Gotify returns so that Gotify
// clients treat the request as successful.
func gotifyResponse(c *gin.Context, n notify.Notification) {
	c.JSON(http.StatusOK, gin.H{
		"id":       messageID.Add(1),
		"appid":    1,
		"title":    n.Title,
		"message":  n.Body,
		"priority": n.Priority,
		"date":     time.Now().Format(time.RFC3339),
	})
}

// slackResponse mimics Slack's literal "ok" body — some senders check for it.
func slackResponse(c *gin.Context, _ notify.Notification) {
	c.String(http.StatusOK, "ok")
}

// presentedToken extracts the ingest token from anywhere Gotify accepts it:
// X-Gotify-Key header, ?token= query parameter, or Authorization: Bearer.
func presentedToken(c *gin.Context) string {
	if t := c.GetHeader("X-Gotify-Key"); t != "" {
		return t
	}
	if t := c.Query("token"); t != "" {
		return t
	}
	return strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
}

func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Make the logger available to handlers via the request context.
		c.Request = c.Request.WithContext(logging.Into(c.Request.Context(), log))
		start := time.Now()
		c.Next()
		log.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", time.Since(start).Round(time.Millisecond),
			"remote", c.ClientIP(),
		)
	}
}
