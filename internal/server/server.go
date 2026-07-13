package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/thomas-maurice/matrix-notifier/internal/chart"
	"github.com/thomas-maurice/matrix-notifier/internal/ingest/alertmanager"
	"github.com/thomas-maurice/matrix-notifier/internal/ingest/gitea"
	"github.com/thomas-maurice/matrix-notifier/internal/ingest/gotify"
	"github.com/thomas-maurice/matrix-notifier/internal/logging"
	"github.com/thomas-maurice/matrix-notifier/internal/metrics"
	"github.com/thomas-maurice/matrix-notifier/internal/notify"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

// Sender is what the ingest endpoints need from the bot: text notifications,
// notification-with-chart messages (image + caption in one event), and a
// health check.
type Sender interface {
	notify.Sender
	SendWithImage(ctx context.Context, roomID string, n notify.Notification, filename string, png []byte) error
	Healthy() (bool, string)
}

// New builds the HTTP handler exposing the ingest endpoints:
//
//	POST /message       Gotify-compatible (X-Gotify-Key / ?token= / Bearer)
//	POST /alertmanager  Prometheus Alertmanager webhook receiver
//	GET  /health        liveness check reflecting Matrix sync state
//	GET  /metrics       Prometheus metrics
//	GET  /version       Gotify-compatible version probe
//
// Ingest tokens are resolved through the store; each token routes to its
// channel's room. charts may be nil (chart rendering disabled). rl may be
// nil (rate limiting disabled).
func New(log *slog.Logger, sender Sender, st *store.Store, charts *chart.Client, rl *limiters) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(log))

	r.GET("/health", func(c *gin.Context) {
		if ok, reason := sender.Healthy(); ok {
			c.JSON(http.StatusOK, gin.H{"health": "green"})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"health": "red", "reason": reason})
		}
	})
	r.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": "2.6.3", "commit": "matrix-notifier", "buildDate": ""})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/message", handleIngest(st, store.KindGotify, gotify.Parse, sender, gotifyResponse, rl))
	r.POST("/alertmanager", handleAlertmanager(st, sender, charts, rl))
	giteaHandler := handleIngest(st, store.KindGitea, gitea.Parse, sender, nil, rl)
	r.POST("/gitea", giteaHandler)
	r.POST("/forgejo", giteaHandler)

	return r
}

// handleAlertmanager delivers the formatted notification. When the channel
// has charts enabled AND an alert opted in (annotation `chart: true`), the
// notification is delivered asynchronously as a single image-with-caption
// message; if the chart cannot be rendered it degrades to plain text. The
// chart is best-effort: its failure must never fail the webhook.
func handleAlertmanager(st *store.Store, sender Sender, charts *chart.Client, rl *limiters) gin.HandlerFunc {
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

		if channel.Chart && charts != nil {
			if target := alertmanager.ChartTarget(payload); target != nil {
				// Detached from the request context: the webhook gets its
				// response now, the combined message follows when Prometheus
				// answers.
				go sendWithChart(logging.From(c.Request.Context()), sender, charts, channel, n, target)
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
				return
			}
		}

		if err := sender.Send(c.Request.Context(), channel.RoomID, n); err != nil {
			metrics.NotificationsFailed.WithLabelValues(channel.Name, kind).Inc()
			logging.From(c.Request.Context()).Error("failed to deliver notification", "channel", channel.Name, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error", "errorCode": 500, "errorDescription": err.Error()})
			return
		}
		metrics.NotificationsDelivered.WithLabelValues(channel.Name, kind).Inc()
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
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

func sendWithChart(log *slog.Logger, sender Sender, charts *chart.Client, channel *store.Channel, n notify.Notification, target *alertmanager.Alert) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = logging.Into(ctx, log)

	start := time.Now()
	png, expr, err := charts.ChartForAlert(ctx, target.GeneratorURL, target.StartsAt)
	metrics.ChartDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.ChartRenders.WithLabelValues("failure").Inc()
		log.Warn("chart rendering failed, delivering text only", "channel", channel.Name, "error", err)
		if err := sender.Send(ctx, channel.RoomID, n); err != nil {
			metrics.NotificationsFailed.WithLabelValues(channel.Name, "alertmanager").Inc()
			log.Error("failed to deliver notification", "channel", channel.Name, "error", err)
			return
		}
		metrics.NotificationsDelivered.WithLabelValues(channel.Name, "alertmanager").Inc()
		return
	}
	metrics.ChartRenders.WithLabelValues("success").Inc()
	name := target.Labels["alertname"]
	if name == "" {
		name = "alert"
	}
	if err := sender.SendWithImage(ctx, channel.RoomID, n, fmt.Sprintf("%s.png", name), png); err != nil {
		metrics.NotificationsFailed.WithLabelValues(channel.Name, "alertmanager").Inc()
		log.Error("failed to send chart notification", "channel", channel.Name, "error", err)
		return
	}
	metrics.NotificationsDelivered.WithLabelValues(channel.Name, "alertmanager").Inc()
	log.Info("notification delivered with chart", "channel", channel.Name, "expr", expr)
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

func handleIngest(st *store.Store, kind store.TokenKind, parse parseFunc, sender notify.Sender, respond responseFunc, rl *limiters) gin.HandlerFunc {
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
		if err := sender.Send(c.Request.Context(), token.Channel.RoomID, n); err != nil {
			metrics.NotificationsFailed.WithLabelValues(token.Channel.Name, string(kind)).Inc()
			logging.From(c.Request.Context()).Error("failed to deliver notification", "channel", token.Channel.Name, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error", "errorCode": 500, "errorDescription": err.Error()})
			return
		}
		metrics.NotificationsDelivered.WithLabelValues(token.Channel.Name, string(kind)).Inc()
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
